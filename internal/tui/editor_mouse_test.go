package tui

import "testing"

func editorModel(body string, w, h int) Model {
	m := New()
	m.width = 90 // single-pane
	m.height = h + 11
	m.bodyArea.SetWidth(w)
	m.bodyArea.SetHeight(h)
	m.bodyArea.SetValue(body)
	m.newFocus = 2
	m.editorYOffset = 0
	// SetValue leaves the cursor at the end; start from the top like a
	// freshly opened editor.
	for m.bodyArea.Line() > 0 {
		m.bodyArea.CursorUp()
	}
	m.bodyArea.SetCursor(0)
	return m
}

func TestClickMovesCursorToLine(t *testing.T) {
	m := editorModel("line one\nline two\nline three", 40, 10)
	m = m.moveEditorCursorTo(2, 5)
	if got := m.bodyArea.Line(); got != 2 {
		t.Errorf("cursor line = %d, want 2", got)
	}
	if got := m.bodyArea.LineInfo().ColumnOffset; got != 5 {
		t.Errorf("cursor col = %d, want 5", got)
	}
}

func TestClickOnSoftWrappedRow(t *testing.T) {
	// width 10 wraps "aaaa bbbb cccc" onto multiple visual rows of line 0
	m := editorModel("aaaa bbbb cccc\nsecond", 10, 10)
	m = m.moveEditorCursorTo(1, 0) // second visual row, still logical line 0
	if got := m.bodyArea.Line(); got != 0 {
		t.Errorf("cursor line = %d, want 0 (soft-wrapped row)", got)
	}
	if got := m.bodyArea.LineInfo().RowOffset; got != 1 {
		t.Errorf("cursor row offset = %d, want 1", got)
	}
}

func TestClickBeyondContentClampsToLastRow(t *testing.T) {
	m := editorModel("only line", 40, 10)
	m = m.moveEditorCursorTo(7, 99)
	if got := m.bodyArea.Line(); got != 0 {
		t.Errorf("cursor line = %d, want 0", got)
	}
}

func TestSyncEditorScrollFollowsCursor(t *testing.T) {
	body := ""
	for i := 0; i < 30; i++ {
		body += "line\n"
	}
	m := editorModel(body, 40, 5)
	for i := 0; i < 20; i++ {
		m.bodyArea.CursorDown()
	}
	m.syncEditorScroll()
	// cursor row 20 must be inside [yoff, yoff+4]
	if m.editorYOffset > 20 || 20 > m.editorYOffset+4 {
		t.Errorf("editorYOffset = %d does not keep row 20 visible", m.editorYOffset)
	}
}
