package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/aeon022/notectl/internal/models"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "notes.db")
	s, err := New(path, false)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// The real-world case ListFolderInfo/ReplaceFolders exist for: an Apple
// Notes folder with zero notes in it has no row anywhere in `notes`, so
// without the separate `folders` table it's invisible to the cache
// entirely — see migrate's doc comment. This pins the union at the
// unscoped/global level (what "All accounts" uses): a folder with notes
// shows its real count, a folder known only via ReplaceFolders (never had a
// note) still shows up, at count 0.
func TestListFolderInfo_UnionsNoteDerivedAndKnownEmptyFolders(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	now := time.Now()
	if err := s.Upsert(ctx, &models.Note{
		ID: "apple-1", Title: "hat inhalt", Folder: "Notizen",
		Account: "FH Burgenland", Source: "apple", ModTime: now, Created: now,
	}); err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}

	if err := s.ReplaceFolders(ctx, "apple", map[string][]string{
		"FH Burgenland": {"Notizen"},
		"Die Brücke":    {"Archiv"}, // real folder, currently empty, never had a note
	}); err != nil {
		t.Fatalf("ReplaceFolders() error = %v", err)
	}

	infos, err := s.ListFolderInfo(ctx)
	if err != nil {
		t.Fatalf("ListFolderInfo() error = %v", err)
	}

	byFolder := map[string]int{}
	for _, fi := range infos {
		byFolder[fi.Folder] = fi.Count
	}
	if c, ok := byFolder["Notizen"]; !ok || c != 1 {
		t.Errorf("Notizen count = %d, ok=%v, want 1", c, ok)
	}
	if c, ok := byFolder["Archiv"]; !ok || c != 0 {
		t.Errorf("Archiv count = %d, ok=%v, want 0 (known-empty, no notes anywhere)", c, ok)
	}
}

// ListFolderInfoByAccount must scope strictly to the given account — an
// unscoped query merging both would reintroduce exactly the
// two-accounts-same-folder-name ambiguity this whole feature exists to
// remove (see CountByFolder/ListFoldersByAccount's own doc comments for the
// same requirement).
func TestListFolderInfoByAccount_ScopesToOneAccount(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.ReplaceFolders(ctx, "apple", map[string][]string{
		"FH Burgenland": {"Notizen"},
		"Die Brücke":    {"Notizen"},
	}); err != nil {
		t.Fatalf("ReplaceFolders() error = %v", err)
	}

	infos, err := s.ListFolderInfoByAccount(ctx, "Die Brücke")
	if err != nil {
		t.Fatalf("ListFolderInfoByAccount() error = %v", err)
	}
	if len(infos) != 1 || infos[0].Account != "Die Brücke" || infos[0].Folder != "Notizen" {
		t.Errorf("infos = %+v, want exactly one Die Brücke/Notizen entry", infos)
	}
}

// ReplaceFolders is delete-then-reinsert per source (matching
// DeleteBySource+Upsert's own note-sync convention) — a folder removed
// since the last sync must not linger as a phantom empty tab forever.
func TestReplaceFolders_DropsFoldersMissingFromLatestSync(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.ReplaceFolders(ctx, "apple", map[string][]string{
		"FH Burgenland": {"Notizen", "Archiv"},
	}); err != nil {
		t.Fatalf("first ReplaceFolders() error = %v", err)
	}
	// Second sync: "Archiv" was deleted in Notes.app since.
	if err := s.ReplaceFolders(ctx, "apple", map[string][]string{
		"FH Burgenland": {"Notizen"},
	}); err != nil {
		t.Fatalf("second ReplaceFolders() error = %v", err)
	}

	folders, err := s.knownFolders(ctx, "FH Burgenland")
	if err != nil {
		t.Fatalf("knownFolders() error = %v", err)
	}
	if len(folders) != 1 || folders[0] != "Notizen" {
		t.Errorf("knownFolders() = %v, want exactly [Notizen] — Archiv should be gone", folders)
	}
}

// A mirrored note is genuinely two rows (its Apple row and its Obsidian
// mirror row) — an unfiltered combined List must show it once (the Apple
// side), while an explicit source=obsidian filter still sees the real
// vault content. CountByFolder/CountByAccount must agree with List so a
// folder tab's count matches what's actually listed under it.
func TestList_HidesObsidianSideOfMirroredPair(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	now := time.Now()

	if err := s.Upsert(ctx, &models.Note{
		ID: "apple-1", Title: "Groceries", Folder: "Shopping", Account: "iCloud", Source: "apple", ModTime: now, Created: now,
	}); err != nil {
		t.Fatalf("Upsert(apple) error = %v", err)
	}
	if err := s.Upsert(ctx, &models.Note{
		ID: "obs-1", Title: "Groceries", Folder: "Shopping", Source: "obsidian", ModTime: now, Created: now,
	}); err != nil {
		t.Fatalf("Upsert(obsidian) error = %v", err)
	}
	// An unlinked pair (no mirror_links row) isn't a mirror yet — both
	// rows should still show up.
	if err := s.Upsert(ctx, &models.Note{
		ID: "obs-unlinked", Title: "Unrelated", Folder: "Shopping", Source: "obsidian", ModTime: now, Created: now,
	}); err != nil {
		t.Fatalf("Upsert(unlinked) error = %v", err)
	}

	all, err := s.List(ctx, Filter{})
	if err != nil {
		t.Fatalf("List(unfiltered) error = %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("List(unfiltered) before linking = %d notes, want 3 (nothing hidden yet — no mirror_links row): %+v", len(all), all)
	}

	if err := s.UpsertMirrorLink(ctx, MirrorLink{AppleID: "apple-1", AppleHash: "h", ObsidianID: "obs-1", ObsidianHash: "h", LastSynced: now}); err != nil {
		t.Fatalf("UpsertMirrorLink() error = %v", err)
	}

	all, err = s.List(ctx, Filter{})
	if err != nil {
		t.Fatalf("List(unfiltered) error = %v", err)
	}
	ids := map[string]bool{}
	for _, n := range all {
		ids[n.ID] = true
	}
	if len(all) != 2 || !ids["apple-1"] || !ids["obs-unlinked"] || ids["obs-1"] {
		t.Fatalf("List(unfiltered) after linking = %+v, want [apple-1, obs-unlinked] with obs-1 hidden", all)
	}

	obsOnly, err := s.List(ctx, Filter{Source: "obsidian"})
	if err != nil {
		t.Fatalf("List(source=obsidian) error = %v", err)
	}
	if len(obsOnly) != 2 {
		t.Fatalf("List(source=obsidian) = %d notes, want 2 — an explicit source filter must still see the real vault content", len(obsOnly))
	}

	counts, err := s.CountByFolder(ctx, "")
	if err != nil {
		t.Fatalf("CountByFolder() error = %v", err)
	}
	if counts["Shopping"] != 2 {
		t.Errorf("CountByFolder()[Shopping] = %d, want 2 (matching the deduped List count)", counts["Shopping"])
	}
}

// Pins the boundary-matching behind Filter.Tag: "go" must not match a note
// tagged "golang" (substring trap), a tag match must be case-insensitive,
// and a note with several tags must still match on any one of them.
func TestList_FiltersByTagExactCaseInsensitive(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	now := time.Now()

	notesByID := map[string]models.Note{
		"go-note":     {ID: "go-note", Title: "Go note", Tags: []string{"go", "cli"}, Source: "obsidian"},
		"golang-note": {ID: "golang-note", Title: "Golang note", Tags: []string{"golang"}, Source: "obsidian"},
		"upper-note":  {ID: "upper-note", Title: "Upper note", Tags: []string{"Go"}, Source: "obsidian"},
	}
	for _, n := range notesByID {
		n := n
		n.ModTime, n.Created = now, now
		if err := s.Upsert(ctx, &n); err != nil {
			t.Fatalf("Upsert(%s) error = %v", n.ID, err)
		}
	}

	got, err := s.List(ctx, Filter{Tag: "go"})
	if err != nil {
		t.Fatalf("List(tag=go) error = %v", err)
	}
	ids := map[string]bool{}
	for _, n := range got {
		ids[n.ID] = true
	}
	if len(got) != 2 || !ids["go-note"] || !ids["upper-note"] || ids["golang-note"] {
		t.Fatalf("List(tag=go) = %+v, want exactly [go-note, upper-note] (golang-note must NOT match on substring)", got)
	}
}

// Pins Filter.EventID as an exact, case-sensitive match — unlike Tag, an
// event id is an opaque identifier from calctl, not a human-typed word, so
// "EVT-1" must not match "evt-1".
func TestList_FiltersByEventIDExactCaseSensitive(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	now := time.Now()

	notesByID := map[string]models.Note{
		"linked-note":    {ID: "linked-note", Title: "Linked note", EventID: "evt-1", Source: "obsidian"},
		"other-note":     {ID: "other-note", Title: "Other note", EventID: "evt-2", Source: "obsidian"},
		"unlinked-note":  {ID: "unlinked-note", Title: "Unlinked note", Source: "obsidian"},
		"mismatch-cased": {ID: "mismatch-cased", Title: "Mismatch cased", EventID: "EVT-1", Source: "obsidian"},
	}
	for _, n := range notesByID {
		n := n
		n.ModTime, n.Created = now, now
		if err := s.Upsert(ctx, &n); err != nil {
			t.Fatalf("Upsert(%s) error = %v", n.ID, err)
		}
	}

	got, err := s.List(ctx, Filter{EventID: "evt-1"})
	if err != nil {
		t.Fatalf("List(event_id=evt-1) error = %v", err)
	}
	if len(got) != 1 || got[0].ID != "linked-note" {
		t.Fatalf("List(event_id=evt-1) = %+v, want exactly [linked-note] (case-sensitive, no cross-event/unlinked matches)", got)
	}
}
