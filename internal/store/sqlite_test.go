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
