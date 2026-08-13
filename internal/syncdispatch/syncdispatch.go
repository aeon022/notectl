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
	"github.com/aeon022/notectl/internal/config"
	"github.com/aeon022/notectl/internal/models"
	"github.com/aeon022/notectl/internal/notes"
)

// Params bundles the per-source inputs each backend's List needs. Fields
// unrelated to the requested source are simply ignored.
type Params struct {
	AppleFolder  string
	VaultPath    string
	JoplinFolder string
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
		return notes.List(p.VaultPath)
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

// ParamsFromConfig builds Params from the current config — the common case
// for every caller (TUI, CLI, MCP) since they all read the same settings.
func ParamsFromConfig() Params {
	return Params{
		AppleFolder:  config.AppleFolder(),
		VaultPath:    config.VaultPath(),
		JoplinFolder: config.JoplinFolder(),
	}
}
