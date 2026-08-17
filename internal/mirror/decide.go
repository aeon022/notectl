// Package mirror computes and applies a bidirectional sync between one
// Apple Notes account and one Obsidian vault. Decide is a pure function —
// no I/O, no Apple Notes, no filesystem — so the linking/conflict logic is
// fully unit-testable; Sync (in mirror.go) is the thin orchestration layer
// that feeds it real data and applies its output.
package mirror

import (
	"crypto/sha256"
	"encoding/hex"
	"time"

	"github.com/aeon022/notectl/internal/models"
)

// Link pairs one Apple Notes note with one Obsidian vault note, plus the
// content hash each side had the last time they were reconciled — that's
// what lets Decide tell "changed since last mirror sync" apart from
// "always been different", without relying on cross-platform-comparable
// modification times.
type Link struct {
	AppleID      string
	AppleHash    string
	ObsidianID   string
	ObsidianHash string
}

// Push carries the winning title/body/folder from one side of a linked
// pair, to be written to the other side. ModTime is the source side's real
// modification time — the target's own storage (a freshly written file's
// mtime, an Apple note's own edit timestamp) doesn't preserve it on its
// own, so callers applying a Push need it to stamp the target explicitly.
type Push struct {
	Title   string
	Body    string
	Folder  string
	ModTime time.Time
	Link    Link
}

// PendingDelete records that a linked note disappeared from one side and
// is awaiting a human-confirmed delete on the other — see the design's
// "Deletion confirmation" section for why this is never auto-applied.
type PendingDelete struct {
	AppleID    string
	ObsidianID string
	Title      string
	DeletedOn  string // "apple" | "obsidian"
}

type Decisions struct {
	CreateInObsidian  []models.Note // apple notes with no obsidian match; Folder already the target vault subfolder
	CreateInApple     []models.Note // obsidian notes with no apple match; Folder already the target apple folder path
	PushToApple       []Push
	PushToObsidian    []Push
	NewLinks          []Link // bootstrap matches whose content already agreed — no push needed, just record the link
	NewPendingDeletes []PendingDelete
	RemoveLinks       []Link // links to drop because one (or both) sides are gone
}

// Hash is the content fingerprint stored per side of a Link. Exported so
// callers (Sync, and tests) can compute the same value Decide uses.
func Hash(title, body string) string {
	sum := sha256.Sum256([]byte(title + "\x00" + body))
	return hex.EncodeToString(sum[:])
}

// Decide computes what a mirror pass should do, given the current note
// lists from both sides, the previously persisted links, and the deletions
// still waiting for human confirmation. It performs no I/O and mutates none
// of its inputs.
//
// pending is what stops a deletion from bouncing forever: queuing a pending
// delete also drops the pair's link row, so the *surviving* note would
// otherwise look brand new and unlinked on the next pass and get recreated
// on the side the user just deleted it from. Every ID named by a queued
// pending delete is therefore left entirely alone — no decision at all —
// until `sync --apply-deletes` resolves the queue, after which the note is
// treated as a normal unlinked note again.
func Decide(appleNotes, obsidianNotes []models.Note, links []Link, pending []PendingDelete) Decisions {
	var d Decisions

	appleByID := indexByID(appleNotes)
	obsByID := indexByID(obsidianNotes)

	inLimbo := make(map[string]bool, 2*len(pending))
	for _, p := range pending {
		if p.AppleID != "" {
			inLimbo[p.AppleID] = true
		}
		if p.ObsidianID != "" {
			inLimbo[p.ObsidianID] = true
		}
	}

	linkedApple := make(map[string]bool, len(links))
	linkedObsidian := make(map[string]bool, len(links))

	for _, l := range links {
		_, appleStill := appleByID[l.AppleID]
		_, obsStill := obsByID[l.ObsidianID]
		linkedApple[l.AppleID] = true
		linkedObsidian[l.ObsidianID] = true

		// A pair that's already in the pending-delete queue shouldn't still
		// have a link row (queuing removes it), but if one survived, leave
		// it and both its notes untouched until the queue is resolved.
		if inLimbo[l.AppleID] || inLimbo[l.ObsidianID] {
			continue
		}

		switch {
		case !appleStill && !obsStill:
			// Both sides already gone (e.g. deleted on both before this
			// pass ever ran) — nothing to queue, just drop the stale link.
			d.RemoveLinks = append(d.RemoveLinks, l)
		case !appleStill:
			d.NewPendingDeletes = append(d.NewPendingDeletes, PendingDelete{
				AppleID: l.AppleID, ObsidianID: l.ObsidianID,
				Title: obsByID[l.ObsidianID].Title, DeletedOn: "apple",
			})
			d.RemoveLinks = append(d.RemoveLinks, l)
		case !obsStill:
			d.NewPendingDeletes = append(d.NewPendingDeletes, PendingDelete{
				AppleID: l.AppleID, ObsidianID: l.ObsidianID,
				Title: appleByID[l.AppleID].Title, DeletedOn: "obsidian",
			})
			d.RemoveLinks = append(d.RemoveLinks, l)
		default:
			a, o := appleByID[l.AppleID], obsByID[l.ObsidianID]
			aChanged := Hash(a.Title, a.Body) != l.AppleHash
			oChanged := Hash(o.Title, o.Body) != l.ObsidianHash
			switch {
			case !aChanged && !oChanged:
				// nothing to do
			case aChanged && !oChanged:
				d.PushToObsidian = append(d.PushToObsidian, Push{Title: a.Title, Body: a.Body, Folder: a.Folder, ModTime: a.ModTime, Link: l})
			case oChanged && !aChanged:
				d.PushToApple = append(d.PushToApple, Push{Title: o.Title, Body: o.Body, Folder: o.Folder, ModTime: o.ModTime, Link: l})
			default: // both changed since the last mirror pass: newer ModTime wins
				if a.ModTime.After(o.ModTime) {
					d.PushToObsidian = append(d.PushToObsidian, Push{Title: a.Title, Body: a.Body, Folder: a.Folder, ModTime: a.ModTime, Link: l})
				} else {
					d.PushToApple = append(d.PushToApple, Push{Title: o.Title, Body: o.Body, Folder: o.Folder, ModTime: o.ModTime, Link: l})
				}
			}
		}
	}

	// Bootstrap: link any not-yet-linked pair that shares folder+title —
	// this is what makes a first mirror pass over two vaults/accounts that
	// already have the same note in both not create a duplicate.
	var unlinkedApple []models.Note
	for _, a := range appleNotes {
		if !linkedApple[a.ID] && !inLimbo[a.ID] {
			unlinkedApple = append(unlinkedApple, a)
		}
	}
	var unlinkedObs []models.Note
	for _, o := range obsidianNotes {
		if !linkedObsidian[o.ID] && !inLimbo[o.ID] {
			unlinkedObs = append(unlinkedObs, o)
		}
	}

	matchedObs := make(map[int]bool, len(unlinkedObs))
	var stillUnlinkedApple []models.Note
	for _, a := range unlinkedApple {
		match := -1
		for i, o := range unlinkedObs {
			if !matchedObs[i] && o.Folder == a.Folder && o.Title == a.Title {
				match = i
				break
			}
		}
		if match == -1 {
			stillUnlinkedApple = append(stillUnlinkedApple, a)
			continue
		}
		matchedObs[match] = true
		o := unlinkedObs[match]
		aHash, oHash := Hash(a.Title, a.Body), Hash(o.Title, o.Body)
		switch {
		case aHash == oHash:
			d.NewLinks = append(d.NewLinks, Link{AppleID: a.ID, AppleHash: aHash, ObsidianID: o.ID, ObsidianHash: oHash})
		case a.ModTime.After(o.ModTime):
			d.PushToObsidian = append(d.PushToObsidian, Push{Title: a.Title, Body: a.Body, Folder: a.Folder, ModTime: a.ModTime, Link: Link{AppleID: a.ID, ObsidianID: o.ID}})
		default:
			d.PushToApple = append(d.PushToApple, Push{Title: o.Title, Body: o.Body, Folder: o.Folder, ModTime: o.ModTime, Link: Link{AppleID: a.ID, ObsidianID: o.ID}})
		}
	}

	var stillUnlinkedObs []models.Note
	for i, o := range unlinkedObs {
		if !matchedObs[i] {
			stillUnlinkedObs = append(stillUnlinkedObs, o)
		}
	}

	d.CreateInObsidian = append(d.CreateInObsidian, stillUnlinkedApple...)
	d.CreateInApple = append(d.CreateInApple, stillUnlinkedObs...)

	return d
}

func indexByID(notes []models.Note) map[string]models.Note {
	m := make(map[string]models.Note, len(notes))
	for _, n := range notes {
		m[n.ID] = n
	}
	return m
}
