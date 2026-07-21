package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/viewport"
	"github.com/spf13/viper"
)

// withAppleSource temporarily makes config.Source() return SourceApple,
// restoring the previous value afterwards — the Apple-bullet-vs-checklist
// disambiguation this file tests for only applies to that source.
func withAppleSource(t *testing.T) {
	t.Helper()
	prev := viper.GetString("source")
	viper.Set("source", "apple")
	t.Cleanup(func() { viper.Set("source", prev) })
}

func TestChecklistLookup_RealStateOverridesFakeCheckbox(t *testing.T) {
	withAppleSource(t)
	t.Cleanup(func() { currentChecklistState = nil })

	currentChecklistState = map[string]bool{
		"Item One": false,
		"Item Two": true,
	}

	// A confirmed checklist item renders with the real done state...
	if got := renderMDLine("• Item One", 80); !strings.Contains(got, "☐") {
		t.Errorf("unchecked real checklist item should show ☐, got %q", got)
	}
	if got := renderMDLine("• Item Two", 80); !strings.Contains(got, "☑") {
		t.Errorf("checked real checklist item should show ☑, got %q", got)
	}
	// ...but a bullet that isn't in the real checklist state (i.e. a genuine
	// plain bullet Apple Notes never marked as a checklist paragraph) must
	// NOT be faked into a checkbox — that fake-☐-for-everything behavior is
	// exactly the bug this file fixes.
	if got := renderMDLine("• Not a checklist item", 80); strings.Contains(got, "☐") || strings.Contains(got, "☑") {
		t.Errorf("plain (non-checklist) Apple bullet should render as a plain bullet, not a checkbox: %q", got)
	}
}

func TestNextNonBlankLine(t *testing.T) {
	lines := []string{"Header", "", "Item A", "Item B", "", "", "Item C", ""}
	//                    0      1     2         3      4   5     6      7

	tests := []struct {
		name string
		from int
		dir  int
		want int
	}{
		{"down over single blank", 0, 1, 2},
		{"down over double blank", 3, 1, 6},
		{"up over single blank", 2, -1, 0},
		{"up over double blank", 6, -1, 3},
		{"down at trailing blank stays put", 6, 1, 6}, // only blank lines remain after Item C
		{"up at top stays put", 0, -1, 0},
		{"down one step no blank between", 2, 1, 3},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := nextNonBlankLine(lines, tc.from, tc.dir)
			if got != tc.want {
				t.Errorf("nextNonBlankLine(from=%d, dir=%d) = %d, want %d", tc.from, tc.dir, got, tc.want)
			}
		})
	}
}

func TestChecklistLookup_UnknownStateFallsBackToPlainBullet(t *testing.T) {
	withAppleSource(t)
	t.Cleanup(func() { currentChecklistState = nil })
	currentChecklistState = nil // e.g. SQLite read failed / not yet loaded

	got := renderMDLine("• Some item", 80)
	if strings.Contains(got, "☐") || strings.Contains(got, "☑") {
		t.Errorf("with no checklist state available, bullets must not guess a checkbox state: %q", got)
	}
}

func TestToggleCheckboxLine(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"• item", "☑ item"},
		{"- item", "☑ item"},
		{"* item", "☑ item"},
		{"- [ ] open task", "- [x] open task"},
		{"* [ ] open task", "* [x] open task"},
		{"- [x] done task", "- [ ] done task"},
		{"- [X] done task", "- [ ] done task"},
		{"☑ checked item", "☐ checked item"},
		{"☐ unchecked item", "☑ unchecked item"},
		{"  • indented item", "  ☑ indented item"},
		{"\t- [ ] tab task", "\t- [x] tab task"},
		{"\t- [x] tab done", "\t- [ ] tab done"},
		{"  ☑ indented checked", "  ☐ indented checked"},
		{"  ☐ indented open", "  ☑ indented open"},
		{"normal text", "normal text"},
	}

	for _, tc := range tests {
		got := toggleCheckboxLine(tc.in)
		if got != tc.want {
			t.Errorf("toggleCheckboxLine(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestRenderMDLineCheckboxes(t *testing.T) {
	tests := []struct {
		in          string
		wantContain string
	}{
		{"- [ ] open", "☐ "},
		{"- [x] done", "☑ "},
		{"  - [ ] indented", "  "},
		{"  ☑ done box", "  "},
		{"  ☐ open box", "  "},
	}

	for _, tc := range tests {
		got := renderMDLine(tc.in, 80)
		if !strings.Contains(got, tc.wantContain) {
			t.Errorf("renderMDLine(%q) = %q, want it to contain %q", tc.in, got, tc.wantContain)
		}
	}
}

func TestPreprocessAndRenderMarkdownTables(t *testing.T) {
	raw := `Some header text
| Header 1 | Header 2 |
| --- | --- |
| Cell 1 | Cell 2 with very long text that should be capped when width is narrow |`

	out := renderMarkdown(raw, 50)
	if !strings.Contains(out, "│") || !strings.Contains(out, "├") {
		t.Errorf("renderMarkdown did not format table into graphical boxes: %s", out)
	}

	lines := preprocessMarkdownTables(strings.Split(raw, "\n"), 50)
	for _, l := range lines {
		if strings.HasPrefix(l, "│") {
			// Check that no single preprocessed table line exceeds width
			if len([]rune(l)) > 55 {
				t.Errorf("preprocessed table line exceeds bounds: %q", l)
			}
		}
	}
}

func TestRenderScrollbarAlignsGlyphColumn(t *testing.T) {
	vp := viewport.New(20, 5)
	// Content with very different line lengths, and more lines than the
	// viewport height so the scrollbar thumb/track actually renders.
	vp.SetContent("a\nbb\nccccccccccccccccc\nd\nee\nfff\ng")

	out := renderScrollbar(vp, "")
	lines := strings.Split(out, "\n")
	if len(lines) == 0 {
		t.Fatal("expected rendered lines, got none")
	}

	glyphCol := -1
	for i, l := range lines {
		// The glyph is the last rune of each rendered line (track "│" or
		// thumb "█", both single-width). Its byte-rune column should be
		// identical across every line regardless of that line's own text
		// length — a mismatch means the glyph isn't forming a straight bar.
		col := len([]rune(l)) - 1
		if glyphCol == -1 {
			glyphCol = col
			continue
		}
		if col != glyphCol {
			t.Errorf("line %d: glyph at column %d, want %d (same as other lines) — scrollbar not vertically aligned: %q", i, col, glyphCol, l)
		}
	}
}
