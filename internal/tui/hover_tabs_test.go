package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/aeon022/missionctl-core/theme"
	"github.com/aeon022/notectl/internal/models"
	"github.com/charmbracelet/x/ansi"
)

func TestHoveredRowKeepsItsOwnColorsAndHasHoverBackground(t *testing.T) {
	n := models.Note{Title: "Alpha idea", Folder: "Projects/KUIN", Tags: []string{"work"}, ModTime: time.Now()}
	row := formatNoteRow(&n, 70, rowHover, "")

	if strings.Contains(ansi.Strip(row), "▌") {
		t.Error("hover has no accent bar — that marks the selection")
	}
	hoverBg := strings.SplitN(theme.HoverV2.Render("x"), "x", 2)[0]
	if !strings.Contains(row, hoverBg) {
		t.Errorf("hover background missing: %q", row)
	}
	// the tag keeps its own style (it used to be flattened to plain text on the hover background)
	tagOpen := strings.SplitN(styleTag.Render("x"), "x", 2)[0]
	if tagOpen != "" && !strings.Contains(row, tagOpen) {
		t.Errorf("hovered row lost the tag's own color:\n%q", row)
	}
	if got := ansi.Strip(row); !strings.Contains(got, "Alpha idea") || !strings.Contains(got, "#work") {
		t.Errorf("content changed: %q", got)
	}
}

func TestNotebookTabHitsAreExactlyWhatWasDrawn(t *testing.T) {
	now := time.Now()
	m := newNotes(t,
		models.Note{ID: "1", Title: "a", Folder: "Baby", ModTime: now},
		models.Note{ID: "2", Title: "b", Folder: "Change-Management", ModTime: now},
		models.Note{ID: "3", Title: "c", Folder: "Linux", ModTime: now},
		models.Note{ID: "4", Title: "d", Folder: "Notes", ModTime: now},
	)
	for _, w := range []int{200, 80, 50} {
		bar, hits := m.tabBar(w)
		if len(hits) == 0 {
			t.Fatalf("width %d: no tab hits", w)
		}
		cells := []rune(ansi.Strip(bar))
		for _, h := range hits {
			if h.x < 0 || h.x+h.w > len(cells) {
				t.Fatalf("width %d: hit %+v outside the %d drawn cells", w, h, len(cells))
			}
			got := strings.TrimSpace(string(cells[h.x : h.x+h.w]))
			want := m.topLabels()[h.idx]
			if !strings.HasPrefix(got, want) {
				t.Errorf("width %d: tab %d span shows %q, want it to start with %q", w, h.idx, got, want)
			}
		}
	}
}
