package mirror

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aeon022/notectl/internal/models"
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

// appleWrite records one note this pass wrote to Apple Notes, along with
// everything needed to persist its link once the Apple side has been listed
// again — see the "re-list" step in Sync for why the link can't be written
// inline.
type appleWrite struct {
	appleID    string
	obsidianID string
	obsHash    string // the Obsidian side's hash, already read-back content
	title      string // for error messages only
	created    bool   // created vs updated, for the report counters
}

// Sync runs one mirror pass: lists both sides fresh, diffs against the
// persisted link table via Decide, and applies the resulting decisions.
// Deletions are never applied here — see ApplyPendingDeletes.
//
// appleFolder scopes the Apple side the same way vaultPath scopes the
// Obsidian side (both are passed in rather than read from config, so this
// package stays free of config). excludeFolders skips vault subfolders that
// belong to another tool sharing this vault (e.g. a diaryctl "Diary"
// folder) — see notes.List.
func Sync(ctx context.Context, s *store.Store, vaultPath, appleFolder string, excludeFolders []string) (Report, error) {
	var report Report

	appleNotes, err := listAppleScoped(appleFolder)
	if err != nil {
		return report, err
	}
	obsidianNotes, err := notes.List(vaultPath, excludeFolders)
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

	storedPending, err := s.ListMirrorPendingDeletes(ctx)
	if err != nil {
		return report, fmt.Errorf("load pending deletes: %w", err)
	}
	pending := make([]PendingDelete, len(storedPending))
	for i, p := range storedPending {
		pending[i] = PendingDelete{AppleID: p.AppleID, ObsidianID: p.ObsidianID, Title: p.Title, DeletedOn: p.DeletedOn}
	}

	d := Decide(appleNotes, obsidianNotes, links, pending)
	obsByID := indexByID(obsidianNotes)
	obsByPath := make(map[string]models.Note, len(obsidianNotes))
	for _, o := range obsidianNotes {
		obsByPath[o.Path] = o
	}

	// Apple-side hashes can't be computed from what we meant to write: Apple
	// Notes carries the title as the body's own first line and reformats the
	// HTML it's given, so the next pass's Decide would see a different body
	// than the one hashed here and call every mirrored note "changed"
	// forever. Links for Apple writes are therefore deferred until after one
	// re-list below, which is exactly what the next Decide will see.
	var appleWrites []appleWrite

	for _, n := range d.CreateInApple {
		// No appleBody() here: WriteApple's create path prefixes the title
		// onto the body itself (see its doc comment). appleBody is for the
		// update path only, below.
		appleID, err := notes.WriteApple("", n.Title, notes.TextToHTML(stripLeadingTitleHeading(n.Title, n.Body)), n.Folder)
		if err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("create in apple %q: %v", n.Title, err))
			continue
		}
		appleWrites = append(appleWrites, appleWrite{
			appleID: appleID, obsidianID: n.ID, obsHash: Hash(n.Title, n.Body), title: n.Title, created: true,
		})
	}

	for _, n := range d.CreateInObsidian {
		// notes.Write overwrites its target path unconditionally, and two
		// distinct titles can slugify to the same filename — refuse rather
		// than destroy an unrelated note. Anything already listed at this
		// path is by definition a different note: had it been this note's
		// counterpart, Decide would have bootstrap-linked the pair instead
		// of asking for a create.
		if clash, taken := obsByPath[notes.TargetPath(n.Title, n.Folder)]; taken {
			report.Errors = append(report.Errors, fmt.Sprintf(
				"create in obsidian %q (folder %q): would overwrite existing note %q — skipped", n.Title, n.Folder, clash.Path))
			continue
		}
		newNote, err := notes.Write(vaultPath, n.Title, n.Body, n.Tags, n.Folder, n.EventID)
		if err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("create in obsidian %q: %v", n.Title, err))
			continue
		}
		stampModTime(vaultPath, newNote.Path, n.ModTime)
		// Hash what landed on disk, not what we asked for: Write prepends a
		// "# title" heading, and the note's title comes back slugified.
		if err := s.UpsertMirrorLink(ctx, store.MirrorLink{
			AppleID: n.ID, AppleHash: Hash(n.Title, n.Body),
			ObsidianID: newNote.ID, ObsidianHash: Hash(newNote.Title, newNote.Body), LastSynced: time.Now(),
		}); err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("link new obsidian note %q: %v", n.Title, err))
			continue
		}
		obsByPath[newNote.Path] = *newNote
		report.Created++
	}

	for _, p := range d.PushToApple {
		body := appleBody(p.Title, stripLeadingTitleHeading(p.Title, p.Body))
		if err := notes.UpdateBody(p.Link.AppleID, notes.TextToHTML(body)); err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("update apple %q: %v", p.Title, err))
			continue
		}
		appleWrites = append(appleWrites, appleWrite{
			appleID: p.Link.AppleID, obsidianID: p.Link.ObsidianID, obsHash: Hash(p.Title, p.Body), title: p.Title,
		})
	}

	for _, p := range d.PushToObsidian {
		old := obsByID[p.Link.ObsidianID] // zero value if this is a fresh bootstrap link — Tags is then just nil, which is fine
		newNote, err := notes.Write(vaultPath, p.Title, p.Body, old.Tags, p.Folder, old.EventID)
		if err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("update obsidian %q: %v", p.Title, err))
			continue
		}
		stampModTime(vaultPath, newNote.Path, p.ModTime)
		// A title change moves the file to a new path, hence a new ID —
		// clean up the old file so a rename doesn't leave an orphan copy.
		if old.Path != "" && newNote.Path != old.Path {
			_ = notes.Delete(vaultPath, old.Path)
			delete(obsByPath, old.Path)
		}
		if err := s.UpsertMirrorLink(ctx, store.MirrorLink{
			AppleID: p.Link.AppleID, AppleHash: Hash(p.Title, p.Body),
			ObsidianID: newNote.ID, ObsidianHash: Hash(newNote.Title, newNote.Body), LastSynced: time.Now(),
		}); err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("relink obsidian %q: %v", p.Title, err))
			continue
		}
		obsByPath[newNote.Path] = *newNote
		report.Updated++
	}

	persistAppleLinks(ctx, s, appleFolder, appleWrites, &report)

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

// listAppleScoped lists exactly the Apple notes this mirror is allowed to
// touch: the default account only — the spec mirrors "exactly one Apple
// Notes account", and notes.ListApple returns every account's notes, which
// let two accounts' identically-titled notes collide onto one vault file —
// narrowed further to apple_folder when that's configured.
//
// The folder match is done here in Go, on the resolved folder path, rather
// than through ListApple's own folder argument, for the reason
// notes.UpsertApple documents: that AppleScript filter compares the
// caller's path against a folder's leaf name, so a nested "Parent/Child"
// path silently matches nothing — which here would read as "every mirrored
// note was deleted on the Apple side". Both spellings are accepted so the
// knob keeps meaning what it means for the ordinary per-source sync.
func listAppleScoped(appleFolder string) ([]models.Note, error) {
	all, err := notes.ListApple("")
	if err != nil {
		return nil, fmt.Errorf("list apple notes: %w", err)
	}
	account, err := notes.DefaultAccountName()
	if err != nil {
		return nil, fmt.Errorf("resolve apple default account: %w", err)
	}
	var scoped []models.Note
	for _, n := range all {
		if n.Account != account {
			continue
		}
		if appleFolder != "" && n.Folder != appleFolder && leafFolder(n.Folder) != appleFolder {
			continue
		}
		scoped = append(scoped, n)
	}
	return scoped, nil
}

func leafFolder(path string) string {
	if i := strings.LastIndex(path, "/"); i >= 0 {
		return path[i+1:]
	}
	return path
}

// persistAppleLinks records the mirror links for every note written to Apple
// Notes this pass. It re-lists the Apple side once first (only if something
// was actually written) so each link's apple_hash is computed from what
// Apple Notes really stored — which is what the next pass's Decide will
// read. Hashing the intended content instead marks every mirrored note
// "changed" on every subsequent sync.
func persistAppleLinks(ctx context.Context, s *store.Store, appleFolder string, writes []appleWrite, report *Report) {
	if len(writes) == 0 {
		return
	}
	fresh, err := listAppleScoped(appleFolder)
	if err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("re-list apple notes to record %d link(s): %v", len(writes), err))
		return
	}
	freshByID := indexByID(fresh)
	for _, w := range writes {
		n, ok := freshByID[w.appleID]
		if !ok {
			// Written, but outside the mirrored scope (an apple_folder
			// filter that doesn't cover the folder it landed in), so its
			// stored hash can't be trusted — leaving it unlinked is the
			// recoverable failure: the note is there, just not tracked.
			report.Errors = append(report.Errors, fmt.Sprintf(
				"link apple note %q: written note is outside the mirrored folder scope — not linked", w.title))
			continue
		}
		if err := s.UpsertMirrorLink(ctx, store.MirrorLink{
			AppleID: w.appleID, AppleHash: Hash(n.Title, n.Body),
			ObsidianID: w.obsidianID, ObsidianHash: w.obsHash, LastSynced: time.Now(),
		}); err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("link apple note %q: %v", w.title, err))
			continue
		}
		if w.created {
			report.Created++
		} else {
			report.Updated++
		}
	}
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

// stripLeadingTitleHeading removes an Obsidian vault body's own leading
// "# title" heading (added by notes.Write's buildContent, see obsidian.go)
// before the content is pushed to Apple Notes — Apple gets its title
// prefixed separately (WriteApple's create path, or appleBody above), so
// without this the title ended up duplicated at the top of every note
// pushed from Obsidian to Apple. Leaves body untouched if it doesn't start
// with the expected heading (title changed since this vault file was
// written, or the file predates this fix) rather than guess.
func stripLeadingTitleHeading(title, body string) string {
	rest := strings.TrimPrefix(body, "# "+title)
	if rest == body {
		return body
	}
	return strings.TrimLeft(rest, "\n")
}

// stampModTime sets a just-written vault file's mtime to t — the source
// side's real modification time. notes.Write always leaves the OS's own
// write-time on the file, so without this every note mirrored (or pushed)
// in one pass would carry today's timestamp regardless of when the
// original note was actually last edited, clustering the whole batch at
// the top of any mod_time-sorted view. Best-effort: a failure here is
// cosmetic (sort order only), not worth failing the mirror pass over.
func stampModTime(vaultPath, relPath string, t time.Time) {
	if t.IsZero() {
		return
	}
	_ = os.Chtimes(filepath.Join(vaultPath, relPath), t, t)
}

// ApplyPendingDeletes deletes the surviving side's note for every currently
// queued pending delete, then clears each one it successfully applied. A
// per-item failure is left queued for the next attempt rather than silently
// dropped.
func ApplyPendingDeletes(ctx context.Context, s *store.Store, vaultPath string, excludeFolders []string) (applied int, errs []string, err error) {
	pending, err := s.ListMirrorPendingDeletes(ctx)
	if err != nil {
		return 0, nil, fmt.Errorf("list pending deletes: %w", err)
	}
	if len(pending) == 0 {
		return 0, nil, nil
	}

	obsidianNotes, lerr := notes.List(vaultPath, excludeFolders)
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
