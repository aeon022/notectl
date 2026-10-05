package notes

import (
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
