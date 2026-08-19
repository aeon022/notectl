package notes

import (
	"os"
	"path/filepath"
	"testing"
)

func testVault(t *testing.T) string {
	t.Helper()
	vault := t.TempDir()
	// hidden dirs must be skipped during indexing
	if err := os.MkdirAll(filepath.Join(vault, ".obsidian"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vault, ".obsidian", "config.md"), []byte("internal"), 0o644); err != nil {
		t.Fatal(err)
	}
	return vault
}

func TestWriteAndRead(t *testing.T) {
	vault := testVault(t)

	n, err := Write(vault, "Meeting Notes", "Discussed the roadmap.", []string{"work"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if n.Title == "" {
		t.Errorf("written note has no title: %+v", n)
	}

	got, err := Read(vault, "Meeting Notes")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("note not found after write")
	}
}

// TestTitleSurvivesLossySlugRoundTrip guards the mirror-duplication bug:
// slugify() strips emoji/punctuation, so before frontmatter carried the
// real title, re-reading a note reported its lossy slug as the title
// instead — which broke mirror's exact-title bootstrap matching in
// internal/mirror/decide.go and spawned duplicate Apple notes on every
// sync pass instead of linking to the existing one.
func TestTitleSurvivesLossySlugRoundTrip(t *testing.T) {
	vault := testVault(t)
	const title = "👶 Baby-Checkliste — Geburt ca. 28.12.2026 (Graz)"

	if _, err := Write(vault, title, "body", nil, ""); err != nil {
		t.Fatal(err)
	}

	notes, err := List(vault, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 1 {
		t.Fatalf("expected 1 note, got %d", len(notes))
	}
	if notes[0].Title != title {
		t.Errorf("Title = %q, want %q (fell back to the lossy slug)", notes[0].Title, title)
	}
}

func TestWriteIntoFolder(t *testing.T) {
	vault := testVault(t)
	if _, err := Write(vault, "Idea", "body", nil, "inbox"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(vault, "inbox", "Idea.md")); err != nil {
		t.Fatalf("note file not created in folder: %v", err)
	}
}

func TestListSkipsHiddenDirs(t *testing.T) {
	vault := testVault(t)
	if _, err := Write(vault, "Visible", "body", nil, ""); err != nil {
		t.Fatal(err)
	}
	notes, err := List(vault, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 1 {
		t.Fatalf("want 1 note (hidden dirs skipped), got %d: %+v", len(notes), notes)
	}
}

func TestListSkipsExcludedFolders(t *testing.T) {
	vault := testVault(t)
	if _, err := Write(vault, "Visible", "body", nil, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := Write(vault, "2026-08-17", "diaryctl's own entry", nil, "Diary"); err != nil {
		t.Fatal(err)
	}

	notes, err := List(vault, []string{"Diary"})
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 1 || notes[0].Title != "Visible" {
		t.Fatalf("want only the non-excluded note, got %d: %+v", len(notes), notes)
	}

	notes, err = List(vault, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 2 {
		t.Fatalf("without an exclude list, want both notes, got %d: %+v", len(notes), notes)
	}
}

func TestSearch(t *testing.T) {
	vault := testVault(t)
	if _, err := Write(vault, "Recipe", "Pasta with tomato sauce", nil, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := Write(vault, "Journal", "Went for a run", nil, ""); err != nil {
		t.Fatal(err)
	}

	hits, err := Search(vault, "tomato", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Fatalf("want 1 hit for body match, got %d", len(hits))
	}

	// title match, case-insensitive
	hits, _ = Search(vault, "JOURNAL", 10)
	if len(hits) != 1 {
		t.Fatalf("want 1 hit for title match, got %d", len(hits))
	}
}

func TestDelete(t *testing.T) {
	vault := testVault(t)
	if _, err := Write(vault, "Temp", "x", nil, ""); err != nil {
		t.Fatal(err)
	}
	if err := Delete(vault, "Temp.md"); err != nil {
		t.Fatal(err)
	}
	got, _ := Read(vault, "Temp")
	if got != nil {
		t.Fatal("note still readable after delete")
	}
}

func TestSlugify(t *testing.T) {
	// slugify keeps case (filenames mirror titles) and strips unsafe characters
	cases := map[string]string{
		"Meeting Notes":  "Meeting-Notes",
		"What?! A/Path":  "What-APath",
		"  trimmed  ":    "trimmed",
		"Umlauts äöü ok": "Umlauts--ok",
	}
	for in, want := range cases {
		if got := slugify(in); got != want {
			t.Errorf("slugify(%q) = %q, want %q", in, got, want)
		}
	}
}
