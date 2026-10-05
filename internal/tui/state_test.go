package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/aeon022/notectl/internal/models"
)

// press feeds keys through Update. Returned commands are never executed: they
// would hit Apple Notes, the vault or the network.
func press(t *testing.T, m Model, keys ...string) (Model, tea.Cmd) {
	t.Helper()
	var cmd tea.Cmd
	for _, k := range keys {
		var msg tea.KeyPressMsg
		switch k {
		case "enter":
			msg = tea.KeyPressMsg{Code: tea.KeyEnter}
		case "esc":
			msg = tea.KeyPressMsg{Code: tea.KeyEscape}
		case "tab":
			msg = tea.KeyPressMsg{Code: tea.KeyTab}
		case "shift+tab":
			msg = tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}
		case "left":
			msg = tea.KeyPressMsg{Code: tea.KeyLeft}
		case "right":
			msg = tea.KeyPressMsg{Code: tea.KeyRight}
		case "space":
			msg = tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
		default:
			msg = tea.KeyPressMsg{Text: k, Code: []rune(k)[0]}
		}
		var tm tea.Model
		tm, cmd = m.Update(msg)
		m = tm.(Model)
	}
	return m, cmd
}

func newNotes(t *testing.T, ns ...models.Note) Model {
	t.Helper()
	t.Setenv("HOME", t.TempDir()) // saveUIState & friends write under HOME
	m := New("")
	m.width, m.height = 90, 30 // single-pane
	m.allNotes = ns
	m.notes = filterNotes(ns, "")
	return m
}

func sampleNotes() []models.Note {
	now := time.Now()
	return []models.Note{
		{ID: "1", Title: "Zebra plan", Body: "see [[Alpha idea]] and [[Missing]]", Tags: []string{"work", "plan"}, ModTime: now},
		{ID: "2", Title: "Alpha idea", Body: "core thought", Tags: []string{"work"}, ModTime: now.Add(-time.Hour)},
		{ID: "3", Title: "Groceries", Body: "milk, links back to [[Alpha idea]]", Tags: []string{"home"}, ModTime: now.Add(-2 * time.Hour)},
	}
}

func nids(ns []models.Note) string {
	var s []string
	for _, n := range ns {
		s = append(s, n.ID)
	}
	return strings.Join(s, ",")
}

func TestListNavigationAndTwoPaneRouting(t *testing.T) {
	m := newNotes(t, sampleNotes()...)
	if m.isTwoPane() {
		t.Fatal("90 columns must be single-pane")
	}
	m, _ = press(t, m, "k")
	m, _ = press(t, m, "j", "j", "j")
	if m.cursor != 2 {
		t.Errorf("j must clamp at the last note, got %d", m.cursor)
	}
	m, _ = press(t, m, "g")
	if m.cursor != 0 {
		t.Errorf("g → %d", m.cursor)
	}
	m, _ = press(t, m, "G")
	if m.cursor != 2 {
		t.Errorf("G → %d", m.cursor)
	}

	tm, _ := m.Update(tea.WindowSizeMsg{Width: 140, Height: 40})
	m = tm.(Model)
	if !m.isTwoPane() {
		t.Error("140 columns must switch to two-pane")
	}
	// two-pane: moving the cursor refreshes the preview viewport
	m, _ = press(t, m, "g", "j")
	if !strings.Contains(m.pvp.GetContent(), "core thought") {
		t.Errorf("preview must show the note under the cursor, got %q", m.pvp.GetContent())
	}
	tm, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	if tm.(Model).isTwoPane() {
		t.Error("shrinking back below 100 columns must return to single-pane")
	}
}

func TestSearchFilterAndSortToggle(t *testing.T) {
	m := newNotes(t, sampleNotes()...)
	m, _ = press(t, m, "/", "g", "r", "o")
	if !m.searching || nids(m.notes) != "3" {
		t.Fatalf("live filter: searching=%v notes=%s", m.searching, nids(m.notes))
	}
	m, _ = press(t, m, "enter", "esc")
	if m.searchQ != "" || len(m.notes) != 3 {
		t.Errorf("esc must restore the full list: q=%q notes=%s", m.searchQ, nids(m.notes))
	}

	// S toggles sort mode; title mode orders case-insensitively A–Z
	before := m.sortByDate
	m, _ = press(t, m, "S")
	if m.sortByDate == before || !strings.HasPrefix(m.status, "Sort:") {
		t.Errorf("S must toggle the sort mode and say so: date=%v→%v status=%q", before, m.sortByDate, m.status)
	}
	m.sortByDate = false
	m = m.applySortOrder()
	if got := nids(m.notes); got != "2,3,1" { // Alpha idea, Groceries, Zebra plan
		t.Errorf("title sort order = %s, want 2,3,1", got)
	}
}

func TestDeleteConfirmUndoAndFullListSync(t *testing.T) {
	m := newNotes(t, sampleNotes()...)
	m, _ = press(t, m, "j") // note 2

	m, cmd := press(t, m, "d")
	if cmd != nil || m.confirmID != "2" || len(m.notes) != 3 {
		t.Fatalf("first d must only ask: cmd=%v confirm=%q notes=%d", cmd != nil, m.confirmID, len(m.notes))
	}
	m, _ = press(t, m, "k") // any other key cancels
	if m.confirmID != "" {
		t.Error("another key must cancel the pending delete")
	}
	m, _ = press(t, m, "j", "d")
	m, cmd = press(t, m, "d")
	if cmd == nil || nids(m.notes) != "1,3" || m.lastDeleted == nil || m.lastDeleted.ID != "2" {
		t.Fatalf("delete: cmd=%v notes=%s last=%v", cmd != nil, nids(m.notes), m.lastDeleted)
	}
	// the unfiltered list (links, tags, search source) must not keep the note
	if got := nids(m.allNotes); got != "1,3" {
		t.Errorf("allNotes = %s after delete, want 1,3", got)
	}
	if outs := outgoingLinksFor(m.allNotes[0], m.allNotes); len(outs) != 0 {
		t.Errorf("deleted note still shows up as a link target: %v", nids(outs))
	}
	m, cmd = press(t, m, "u")
	if cmd == nil || m.lastDeleted != nil {
		t.Errorf("u must return the undo command once: cmd=%v last=%v", cmd != nil, m.lastDeleted)
	}
}

func TestDeleteWhileFilteredDoesNotReturnAfterClearingSearch(t *testing.T) {
	m := newNotes(t, sampleNotes()...)
	m, _ = press(t, m, "/", "g", "r", "o", "enter") // only Groceries
	if nids(m.notes) != "3" {
		t.Fatalf("setup: filtered notes = %s", nids(m.notes))
	}
	first := m.notes[m.cursor].ID
	m, _ = press(t, m, "d", "d")
	m, _ = press(t, m, "esc")
	for _, n := range m.notes {
		if n.ID == first {
			t.Errorf("deleted note %s came back after clearing the search", first)
		}
	}
}

func TestDetailDeleteSyncsFullList(t *testing.T) {
	m := newNotes(t, sampleNotes()...)
	m, _ = press(t, m, "j", "enter")
	if m.view != viewDetail || m.detail == nil || m.detail.ID != "2" {
		t.Fatalf("enter: view=%v detail=%v", m.view, m.detail)
	}
	m, _ = press(t, m, "d")
	if m.confirmID != "2" {
		t.Fatalf("d in detail must ask first, confirm=%q", m.confirmID)
	}
	m, cmd := press(t, m, "d")
	if m.view != viewList || cmd == nil || nids(m.notes) != "1,3" || nids(m.allNotes) != "1,3" {
		t.Errorf("detail delete: view=%v notes=%s all=%s", m.view, nids(m.notes), nids(m.allNotes))
	}
}

func TestTagBrowser(t *testing.T) {
	tags := allTags(sampleNotes())
	if len(tags) != 3 || tags[0].name != "work" || tags[0].count != 2 {
		t.Fatalf("allTags = %+v, want work(2) first", tags)
	}
	if tags[1].name != "home" || tags[2].name != "plan" { // equal counts sort by name
		t.Errorf("tie order = %+v", tags)
	}

	m := newNotes(t, sampleNotes()...)
	m, _ = press(t, m, "t")
	if m.view != viewTags || m.tagCursor != 0 {
		t.Fatalf("t: view=%v cursor=%d", m.view, m.tagCursor)
	}
	m, _ = press(t, m, "k", "j", "j", "j", "j")
	if m.tagCursor != 2 {
		t.Errorf("tag cursor must clamp, got %d", m.tagCursor)
	}
	m, _ = press(t, m, "k", "k", "enter") // "work"
	if m.view != viewList || m.searchQ != "work" {
		t.Errorf("enter must filter by the tag: view=%v q=%q", m.view, m.searchQ)
	}
	m, _ = press(t, m, "t", "esc")
	if m.view != viewList {
		t.Errorf("esc must leave the tag browser, view=%v", m.view)
	}
}

func TestLinkGraphNavigation(t *testing.T) {
	m := newNotes(t, sampleNotes()...)
	m, _ = press(t, m, "L") // focus Zebra plan: out = Alpha idea (Missing skipped), in = none
	if m.view != viewGraph || m.graphFocus == nil || m.graphFocus.ID != "1" || nids(m.graphOut) != "2" || len(m.graphIn) != 0 {
		t.Fatalf("graph on note 1: view=%v out=%s in=%s", m.view, nids(m.graphOut), nids(m.graphIn))
	}
	m, _ = press(t, m, "enter") // re-focus on Alpha idea: out none, in = Zebra, Groceries
	if m.graphFocus.ID != "2" || len(m.graphOut) != 0 || nids(m.graphIn) != "1,3" {
		t.Fatalf("refocus: focus=%s out=%s in=%s", m.graphFocus.ID, nids(m.graphOut), nids(m.graphIn))
	}
	m, _ = press(t, m, "j", "j", "j") // clamp at last neighbor
	if m.graphCursor != 1 {
		t.Errorf("graph cursor must clamp at 1, got %d", m.graphCursor)
	}
	m, _ = press(t, m, "d") // open neighbor 3 in detail
	if m.view != viewDetail || m.detail.ID != "3" {
		t.Fatalf("d must open the neighbor: view=%v detail=%v", m.view, m.detail)
	}
	m.view, m.graphPrevView = viewGraph, viewList
	m, _ = press(t, m, "esc")
	if m.view != viewList {
		t.Errorf("esc must return to where L was pressed, view=%v", m.view)
	}
}

func TestNewAndEditNoteFlow(t *testing.T) {
	m := newNotes(t, sampleNotes()...)
	m, _ = press(t, m, "n")
	if m.view != viewNew || m.editNote != nil || m.newFocus != 0 {
		t.Fatalf("n: view=%v edit=%v focus=%d", m.view, m.editNote, m.newFocus)
	}
	m, _ = press(t, m, "H", "i", "tab", "a", ",", "b", "tab", "x")
	if m.titleInput.Value() != "Hi" || m.tagsInput.Value() != "a,b" || m.bodyArea.Value() != "x" || m.newFocus != 2 {
		t.Errorf("typed into wrong fields: title=%q tags=%q body=%q focus=%d", m.titleInput.Value(), m.tagsInput.Value(), m.bodyArea.Value(), m.newFocus)
	}
	m, _ = press(t, m, "tab")
	if m.newFocus != 2 {
		t.Errorf("tab past the body moved focus to %d", m.newFocus)
	}
	m, _ = press(t, m, "shift+tab", "shift+tab", "shift+tab")
	if m.newFocus != 0 {
		t.Errorf("shift+tab past the title moved focus to %d", m.newFocus)
	}
	if _, cmd := press(t, m, "esc"); cmd != nil {
		t.Error("esc must discard without a command")
	}
	m, _ = press(t, m, "esc")
	if m.view != viewList {
		t.Fatalf("esc: view=%v", m.view)
	}

	// e prefills the editor from the selected note
	m, _ = press(t, m, "e")
	if m.view != viewNew || m.editNote == nil || m.editNote.ID != "1" {
		t.Fatalf("e: view=%v edit=%v", m.view, m.editNote)
	}
	if m.titleInput.Value() != "Zebra plan" || m.tagsInput.Value() != "work, plan" || !strings.Contains(m.bodyArea.Value(), "[[Alpha idea]]") {
		t.Errorf("edit prefill: title=%q tags=%q", m.titleInput.Value(), m.tagsInput.Value())
	}
}

func TestSettingsSourcePicker(t *testing.T) {
	m := newNotes(t, sampleNotes()...)
	m, _ = press(t, m, "p")
	if m.view != viewSettings {
		t.Fatalf("p: view=%v", m.view)
	}
	start := m.sourceIdx
	m, _ = press(t, m, "right")
	if m.sourceIdx != min(start+1, len(sourceTypes)-1) {
		t.Errorf("right: %d → %d", start, m.sourceIdx)
	}
	for range sourceTypes {
		m, _ = press(t, m, "right")
	}
	if m.sourceIdx != len(sourceTypes)-1 {
		t.Errorf("right must clamp at the last source, got %d", m.sourceIdx)
	}
	for range sourceTypes {
		m, _ = press(t, m, "left")
	}
	if m.sourceIdx != 0 {
		t.Errorf("left must clamp at 0, got %d", m.sourceIdx)
	}
	m, _ = press(t, m, "esc")
	if m.view != viewList {
		t.Errorf("esc: view=%v", m.view)
	}
}

func TestPaletteRunsCommand(t *testing.T) {
	m := newNotes(t, sampleNotes()...)
	m, _ = press(t, m, ":")
	if !m.inPalette {
		t.Fatal(": must open the palette")
	}
	m, _ = press(t, m, "esc")
	if m.inPalette {
		t.Error("esc must close it")
	}
}
