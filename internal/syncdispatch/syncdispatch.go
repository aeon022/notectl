// Package syncdispatch is the one place that knows which notes.List*
// function to call for a given config.SourceType. Previously that switch
// was copy-pasted three times (TUI sync, `notectl sync`, the sync_notes MCP
// tool) — adding Joplin meant touching all three, and the MCP one had
// silently drifted to only ever sync Obsidian regardless of the configured
// source. Centralizing it here means a future fourth source only needs
// wiring in one place, and multi-source sync (looping over
// config.SyncSources()) doesn't need its own copy of the switch either.
package syncdispatch

import (
	"time"

	"github.com/aeon022/notectl/internal/config"
	"github.com/aeon022/notectl/internal/models"
	"github.com/aeon022/notectl/internal/notes"
)

// Params bundles the per-source inputs each backend's List needs. Fields
// unrelated to the requested source are simply ignored.
type Params struct {
	AppleFolder    string
	VaultPath      string
	JoplinFolder   string
	ExcludeFolders []string
}

// SourceKey is the store's `source` column value for src. Kept distinct
// from config.SourceType because SourceObsidian and SourceMarkdown share
// one backend (notes.List) but both tag their cache rows "obsidian",
// matching the convention already in place before Joplin existed.
func SourceKey(src config.SourceType) string {
	switch src {
	case config.SourceApple:
		return "apple"
	case config.SourceJoplin:
		return "joplin"
	default:
		return "obsidian"
	}
}

// List fetches every note from exactly one configured source.
func List(src config.SourceType, p Params) ([]models.Note, error) {
	switch src {
	case config.SourceApple:
		return notes.ListApple(p.AppleFolder)
	case config.SourceJoplin:
		return notes.ListJoplin(p.JoplinFolder)
	default:
		return notes.List(p.VaultPath, p.ExcludeFolders)
	}
}

// SyncFolders returns src's full per-account folder listing — including
// folders with zero notes in them — for callers to feed into
// store.ReplaceFolders alongside their regular List/Upsert sync. nil, nil
// for any source other than Apple: Obsidian/Joplin folders always get
// discovered through List's own notes (a note's folder is a plain vault
// subdirectory, not an account-scoped, possibly-empty, possibly-name-
// colliding notebook the way Apple Notes folders are), so there's nothing
// this needs to add for them.
func SyncFolders(src config.SourceType) (map[string][]string, error) {
	if src != config.SourceApple {
		return nil, nil
	}
	return notes.ListAppleAccountFolders()
}

// WriteParams bundles per-source inputs for WriteBySource. Not every field
// applies to every source.
type WriteParams struct {
	// ID is the existing note's id to update in place. Joplin and (when
	// AppleBody is set) Apple use it directly; empty creates a new note.
	// Obsidian/Markdown's notes.Write always matches by title/slug instead,
	// regardless of ID.
	ID     string
	Title  string
	Body   string
	Tags   []string
	Folder string
	// AppleBody, when set, is the exact (already HTML, possibly block-
	// reconciled) body to send via notes.WriteApple(ID, ...) — used by the
	// TUI editor, which already knows the note's id and does its own
	// title-prefix/block-reconciliation prep before calling in. Left empty,
	// Apple instead looks the note up by title and creates-or-updates it
	// via notes.UpsertApple — the plain create/update path `notectl write`
	// and the write_note MCP tool use, neither of which ever has an id or
	// pre-rendered HTML to give.
	AppleBody string
	VaultPath string
}

// WriteBySource writes a note to src's real backend and returns the
// resulting note, ready to hand to store.Upsert — the write-side pair to
// List above. Centralizes what was previously the same switch on
// config.Source() duplicated in cmd/write.go, mcpserver's handleWrite, and
// the TUI's writeNoteCmd.
func WriteBySource(src config.SourceType, p WriteParams) (*models.Note, error) {
	now := time.Now()
	switch src {
	case config.SourceApple:
		if p.AppleBody == "" {
			id, err := notes.UpsertApple(p.Title, p.Body, p.Folder)
			if err != nil {
				return nil, err
			}
			return &models.Note{ID: id, Title: p.Title, Body: p.Body, Tags: p.Tags, Folder: p.Folder, Source: "apple", ModTime: now, Created: now}, nil
		}
		id, err := notes.WriteApple(p.ID, p.Title, p.AppleBody, p.Folder)
		if err != nil {
			return nil, err
		}
		return &models.Note{ID: id, Title: p.Title, Body: p.Body, Tags: p.Tags, Folder: p.Folder, Source: "apple", ModTime: now, Created: now}, nil
	case config.SourceJoplin:
		id, err := notes.WriteJoplin(p.ID, p.Title, p.Body, p.Folder)
		if err != nil {
			return nil, err
		}
		return &models.Note{ID: id, Title: p.Title, Body: p.Body, Tags: p.Tags, Folder: p.Folder, Source: "joplin", ModTime: now, Created: now}, nil
	default:
		return notes.Write(p.VaultPath, p.Title, p.Body, p.Tags, p.Folder)
	}
}

// DeleteBySource deletes a note by id from src's real backend — the
// delete-side pair to WriteBySource above. vaultPath/path are only used for
// the Obsidian/Markdown default (notes.Delete); an empty path there deletes
// vaultPath itself, so callers that can see an empty path (the TUI's
// deleteNoteCmd) must guard against that themselves before calling in —
// the MCP handler never sees an empty path since it always comes from a
// real cached note row.
func DeleteBySource(src config.SourceType, id, vaultPath, path string) error {
	switch src {
	case config.SourceApple:
		return notes.DeleteApple(id)
	case config.SourceJoplin:
		return notes.DeleteJoplin(id)
	default:
		return notes.Delete(vaultPath, path)
	}
}

// ParamsFromConfig builds Params from the current config — the common case
// for every caller (TUI, CLI, MCP) since they all read the same settings.
func ParamsFromConfig() Params {
	return Params{
		AppleFolder:    config.AppleFolder(),
		VaultPath:      config.VaultPath(),
		JoplinFolder:   config.JoplinFolder(),
		ExcludeFolders: config.ExcludeFolders(),
	}
}
