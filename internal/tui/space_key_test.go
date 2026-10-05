package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/aeon022/notectl/internal/models"
)

// Bubble Tea v2 reports a space press as String() == "space" (v1: " "). A
// `case " ":` handler silently never fires — this pins that a real v2 space
// key press reaches the checkbox toggle in the detail view.
func TestSpaceKeyTogglesCheckboxInDetailView(t *testing.T) {
	m := New("")
	m.width, m.height = 100, 40
	n := models.Note{ID: "n1", Title: "Todo", Body: "- [ ] eins\n- [ ] zwei"}
	m.notes = []models.Note{n}
	m.detail = &n
	m.view = viewDetail
	m.detailLineCursor = 1

	got, _ := m.Update(tea.KeyPressMsg{Code: tea.KeySpace, Text: " "})
	gm := got.(Model)

	if want := "- [ ] eins\n- [x] zwei"; gm.detail.Body != want {
		t.Errorf("space must toggle the cursor line:\n got %q\nwant %q", gm.detail.Body, want)
	}
	if !strings.Contains(gm.notes[0].Body, "- [x] zwei") {
		t.Error("the cached list copy of the note must be updated too")
	}
	if s := (tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}).String(); s != "space" {
		t.Errorf("bubbletea v2 space key String() = %q — handler cases must match this", s)
	}
}
