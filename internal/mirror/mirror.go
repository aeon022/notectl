package mirror

import (
	"context"
	"fmt"
	"time"

	"github.com/aeon022/notectl/internal/notes"
	"github.com/aeon022/notectl/internal/store"
)

// Report summarizes one mirror.Sync pass for CLI/TUI/MCP output.
type Report struct {
	Created        int // new notes written to either side
	Updated        int // existing notes pushed to the other side
	LinkedExisting int // bootstrap matches that needed no content push
	PendingDeletes int // newly queued this pass — see ApplyPendingDeletes
	Errors         []string
}

// Sync runs one mirror pass: lists both sides fresh, diffs against the
// persisted link table via Decide, and applies the resulting decisions.
// Deletions are never applied here — see ApplyPendingDeletes.
func Sync(ctx context.Context, s *store.Store, vaultPath string) (Report, error) {
	var report Report

	appleNotes, err := notes.ListApple("")
	if err != nil {
		return report, fmt.Errorf("list apple notes: %w", err)
	}
	obsidianNotes, err := notes.List(vaultPath)
	if err != nil {
		return report, fmt.Errorf("list vault: %w", err)
	}

	storedLinks, err := s.ListMirrorLinks(ctx)
	if err != nil {
		return report, fmt.Errorf("load mirror links: %w", err)
	}
	links := make([]Link, len(storedLinks))
	for i, l := range storedLinks {
		links[i] = Link{AppleID: l.AppleID, AppleHash: l.AppleHash, ObsidianID: l.ObsidianID, ObsidianHash: l.ObsidianHash}
	}

	d := Decide(appleNotes, obsidianNotes, links)
	obsByID := indexByID(obsidianNotes)

	for _, n := range d.CreateInApple {
		appleID, err := notes.WriteApple("", n.Title, notes.TextToHTML(appleBody(n.Title, n.Body)), n.Folder)
		if err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("create in apple %q: %v", n.Title, err))
			continue
		}
		h := Hash(n.Title, n.Body)
		if err := s.UpsertMirrorLink(ctx, store.MirrorLink{AppleID: appleID, AppleHash: h, ObsidianID: n.ID, ObsidianHash: h, LastSynced: time.Now()}); err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("link new apple note %q: %v", n.Title, err))
			continue
		}
		report.Created++
	}

	for _, n := range d.CreateInObsidian {
		newNote, err := notes.Write(vaultPath, n.Title, n.Body, n.Tags, n.Folder)
		if err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("create in obsidian %q: %v", n.Title, err))
			continue
		}
		h := Hash(n.Title, n.Body)
		if err := s.UpsertMirrorLink(ctx, store.MirrorLink{AppleID: n.ID, AppleHash: h, ObsidianID: newNote.ID, ObsidianHash: h, LastSynced: time.Now()}); err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("link new obsidian note %q: %v", n.Title, err))
			continue
		}
		report.Created++
	}

	for _, p := range d.PushToApple {
		if err := notes.UpdateBody(p.Link.AppleID, notes.TextToHTML(appleBody(p.Title, p.Body))); err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("update apple %q: %v", p.Title, err))
			continue
		}
		h := Hash(p.Title, p.Body)
		if err := s.UpsertMirrorLink(ctx, store.MirrorLink{AppleID: p.Link.AppleID, AppleHash: h, ObsidianID: p.Link.ObsidianID, ObsidianHash: h, LastSynced: time.Now()}); err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("relink apple %q: %v", p.Title, err))
			continue
		}
		report.Updated++
	}

	for _, p := range d.PushToObsidian {
		old := obsByID[p.Link.ObsidianID] // zero value if this is a fresh bootstrap link — Tags is then just nil, which is fine
		newNote, err := notes.Write(vaultPath, p.Title, p.Body, old.Tags, p.Folder)
		if err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("update obsidian %q: %v", p.Title, err))
			continue
		}
		// A title change moves the file to a new path, hence a new ID —
		// clean up the old file so a rename doesn't leave an orphan copy.
		if old.Path != "" && newNote.Path != old.Path {
			_ = notes.Delete(vaultPath, old.Path)
		}
		h := Hash(p.Title, p.Body)
		if err := s.UpsertMirrorLink(ctx, store.MirrorLink{AppleID: p.Link.AppleID, AppleHash: h, ObsidianID: newNote.ID, ObsidianHash: h, LastSynced: time.Now()}); err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("relink obsidian %q: %v", p.Title, err))
			continue
		}
		report.Updated++
	}

	for _, l := range d.NewLinks {
		if err := s.UpsertMirrorLink(ctx, store.MirrorLink{AppleID: l.AppleID, AppleHash: l.AppleHash, ObsidianID: l.ObsidianID, ObsidianHash: l.ObsidianHash, LastSynced: time.Now()}); err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("link existing pair %s/%s: %v", l.AppleID, l.ObsidianID, err))
			continue
		}
		report.LinkedExisting++
	}

	for _, l := range d.RemoveLinks {
		_ = s.DeleteMirrorLink(ctx, l.AppleID)
	}

	for _, p := range d.NewPendingDeletes {
		if err := s.AddMirrorPendingDelete(ctx, store.MirrorPendingDelete{
			AppleID: p.AppleID, ObsidianID: p.ObsidianID, Title: p.Title, DeletedOn: p.DeletedOn, DetectedAt: time.Now(),
		}); err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("queue pending delete %q: %v", p.Title, err))
			continue
		}
		report.PendingDeletes++
	}

	return report, nil
}

// appleBody prefixes title into body the same way notes.UpsertApple's
// update path does — WriteApple/UpdateBody's HTML body needs the title as
// its own first line, or Apple Notes displays a blank/mismatched title.
func appleBody(title, body string) string {
	if body == "" {
		return title
	}
	return title + "\n\n" + body
}

// ApplyPendingDeletes deletes the surviving side's note for every currently
// queued pending delete, then clears each one it successfully applied. A
// per-item failure is left queued for the next attempt rather than silently
// dropped.
func ApplyPendingDeletes(ctx context.Context, s *store.Store, vaultPath string) (applied int, errs []string, err error) {
	pending, err := s.ListMirrorPendingDeletes(ctx)
	if err != nil {
		return 0, nil, fmt.Errorf("list pending deletes: %w", err)
	}
	if len(pending) == 0 {
		return 0, nil, nil
	}

	obsidianNotes, lerr := notes.List(vaultPath)
	if lerr != nil {
		return 0, nil, fmt.Errorf("list vault: %w", lerr)
	}
	obsByID := indexByID(obsidianNotes)

	for _, p := range pending {
		var delErr error
		switch p.DeletedOn {
		case "apple":
			if old, ok := obsByID[p.ObsidianID]; ok {
				delErr = notes.Delete(vaultPath, old.Path)
			}
		case "obsidian":
			delErr = notes.DeleteApple(p.AppleID)
		}
		if delErr != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", p.Title, delErr))
			continue
		}
		if err := s.DeleteMirrorPendingDelete(ctx, p.ID); err != nil {
			errs = append(errs, fmt.Sprintf("%s: clearing queue entry: %v", p.Title, err))
			continue
		}
		applied++
	}
	return applied, errs, nil
}
