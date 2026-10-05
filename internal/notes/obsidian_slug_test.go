package notes

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSlugifyAndTargetPath(t *testing.T) {
	if got := slugify("  Mein Plan_2026 (v2)! "); got != "Mein-Plan_2026-v2" {
		t.Errorf("slugify = %q", got)
	}
	if got := TargetPath("Plan B", "Projekte/Alt"); got != "Projekte/Alt/Plan-B.md" {
		t.Errorf("TargetPath = %q", got)
	}
}

// A title with no ASCII letters used to slugify to "" → the file ".md",
// shared by every such note (silent overwrite).
func TestSlugifyNonASCIITitleNeverEmptyAndStable(t *testing.T) {
	a, b := slugify("日本語"), slugify("한국어")
	if a == "" || b == "" || !strings.HasPrefix(a, "note-") {
		t.Fatalf("slug = %q / %q", a, b)
	}
	if a == b {
		t.Error("different titles must not collide")
	}
	if a != slugify("日本語") {
		t.Error("slug must be stable across calls")
	}
	if TargetPath("日本語", "") == ".md" {
		t.Error("TargetPath must not be .md")
	}
}

func TestDeleteRefusesPathsOutsideTheNote(t *testing.T) {
	vault := t.TempDir()
	outside := filepath.Join(filepath.Dir(vault), "outside-"+filepath.Base(vault)+".md")
	if err := os.WriteFile(outside, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(outside)
	for _, rel := range []string{"", ".", "../" + filepath.Base(outside), outside} {
		if err := Delete(vault, rel); err == nil {
			t.Errorf("Delete(%q) must be refused", rel)
		}
	}
	if _, err := os.Stat(vault); err != nil {
		t.Error("vault dir must survive an empty path")
	}
	if _, err := os.Stat(outside); err != nil {
		t.Error("file outside the vault must survive")
	}
	f := filepath.Join(vault, "n.md")
	_ = os.WriteFile(f, []byte("x"), 0o644)
	if err := Delete(vault, "n.md"); err != nil {
		t.Fatalf("normal delete: %v", err)
	}
	if _, err := os.Stat(f); !os.IsNotExist(err) {
		t.Error("note not deleted")
	}
}
