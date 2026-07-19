package tui

import (
	"strings"
	"unicode"

	rw "github.com/mattn/go-runewidth"
	"github.com/rivo/uniseg"
)

// Mouse support for the new/edit view: click-to-position the cursor in the
// body textarea and wheel scrolling. bubbles/textarea does not expose its
// viewport offset, so we mirror its scrolling rule (keep the cursor visible,
// scroll minimally) in editorYOffset and replicate its soft-wrap to map a
// screen position back to a logical row/column.

// editorSyncMsg is a no-op message pushed through bodyArea.Update after we
// move the cursor manually — textarea repositions its viewport on any Update.
type editorSyncMsg struct{}

// Layout rows in renderNew: 0 header, 1 divider, 2 blank, 3 title, 4 tags,
// 5 blank, 6 body label, 7… body textarea.
const (
	editorTitleRow = 3
	editorTagsRow  = 4
	editorBodyTop  = 7
	// textarea prompt "┃ " rendered before each body line
	editorPromptW = 2
)

// handleEditorClick focuses the field under a left click; clicks on the body
// also move the cursor to the clicked position.
func (m Model) handleEditorClick(x, y int) Model {
	if m.isTwoPane() && x > m.leftWidth() {
		return m // click landed in the preview pane
	}
	switch {
	case y == editorTitleRow:
		m.blurNew(m.newFocus)
		m.newFocus = 0
		m.focusNew(0)
	case y == editorTagsRow:
		m.blurNew(m.newFocus)
		m.newFocus = 1
		m.focusNew(1)
	case y >= editorBodyTop && y < editorBodyTop+m.bodyArea.Height():
		if m.newFocus != 2 {
			m.blurNew(m.newFocus)
			m.newFocus = 2
			m.focusNew(2)
		}
		m = m.moveEditorCursorTo(y-editorBodyTop, x-editorPromptW)
	}
	return m
}

// scrollEditor moves the body cursor n visual rows (negative = up), the
// wheel-scroll equivalent for the textarea.
func (m Model) scrollEditor(n int) Model {
	for ; n < 0; n++ {
		m.bodyArea.CursorUp()
	}
	for ; n > 0; n-- {
		m.bodyArea.CursorDown()
	}
	m.bodyArea, _ = m.bodyArea.Update(editorSyncMsg{})
	m.syncEditorScroll()
	return m
}

// moveEditorCursorTo places the cursor on the clicked visible row/column.
func (m Model) moveEditorCursorTo(clickRow, clickX int) Model {
	grid := m.editorVisualGrid()
	total := 0
	for _, g := range grid {
		total += len(g)
	}
	target := m.editorYOffset + clickRow
	if target > total-1 {
		target = total - 1
	}
	if target < 0 {
		target = 0
	}

	// locate logical line and soft-wrap row within it
	logical, rowIn := 0, target
	for logical < len(grid)-1 && rowIn >= len(grid[logical]) {
		rowIn -= len(grid[logical])
		logical++
	}
	if rowIn >= len(grid[logical]) {
		rowIn = len(grid[logical]) - 1
	}

	startCol := 0
	for i := 0; i < rowIn; i++ {
		startCol += len(grid[logical][i])
	}
	seg := grid[logical][rowIn]
	col, w := 0, 0
	for col < len(seg) {
		cw := rw.RuneWidth(seg[col])
		if w+cw > clickX {
			break
		}
		w += cw
		col++
	}

	// move to the logical line, then set the column (SetCursor clamps)
	for guard := 0; m.bodyArea.Line() < logical && guard < 10000; guard++ {
		m.bodyArea.CursorDown()
	}
	for guard := 0; m.bodyArea.Line() > logical && guard < 10000; guard++ {
		m.bodyArea.CursorUp()
	}
	m.bodyArea.SetCursor(startCol + col)
	m.bodyArea, _ = m.bodyArea.Update(editorSyncMsg{})
	m.syncEditorScroll()
	return m
}

// editorVisualGrid soft-wraps every body line exactly like the textarea does.
func (m Model) editorVisualGrid() [][][]rune {
	w := m.bodyArea.Width()
	lines := strings.Split(m.bodyArea.Value(), "\n")
	grid := make([][][]rune, len(lines))
	for i, l := range lines {
		grid[i] = taWrap([]rune(l), w)
	}
	return grid
}

// editorCursorVisualRow is the cursor's row counting soft-wrapped lines.
func (m Model) editorCursorVisualRow() int {
	w := m.bodyArea.Width()
	lines := strings.Split(m.bodyArea.Value(), "\n")
	row := 0
	for i := 0; i < m.bodyArea.Line() && i < len(lines); i++ {
		row += len(taWrap([]rune(lines[i]), w))
	}
	return row + m.bodyArea.LineInfo().RowOffset
}

// syncEditorScroll mirrors textarea.repositionView: scroll just enough to
// keep the cursor row inside the visible window.
func (m *Model) syncEditorScroll() {
	row := m.editorCursorVisualRow()
	h := m.bodyArea.Height()
	if h < 1 {
		h = 1
	}
	if row < m.editorYOffset {
		m.editorYOffset = row
	} else if row > m.editorYOffset+h-1 {
		m.editorYOffset = row - h + 1
	}
	if m.editorYOffset < 0 {
		m.editorYOffset = 0
	}
}

// taWrap is a copy of bubbles/textarea's soft-wrap (MIT) — it must match
// exactly so clicked rows map to the same content the textarea shows.
func taWrap(runes []rune, width int) [][]rune {
	var (
		lines  = [][]rune{{}}
		word   = []rune{}
		row    int
		spaces int
	)

	for _, r := range runes {
		if unicode.IsSpace(r) {
			spaces++
		} else {
			word = append(word, r)
		}

		if spaces > 0 {
			if uniseg.StringWidth(string(lines[row]))+uniseg.StringWidth(string(word))+spaces > width {
				row++
				lines = append(lines, []rune{})
				lines[row] = append(lines[row], word...)
				lines[row] = append(lines[row], taSpaces(spaces)...)
			} else {
				lines[row] = append(lines[row], word...)
				lines[row] = append(lines[row], taSpaces(spaces)...)
			}
			spaces = 0
			word = nil
		} else {
			lastCharLen := rw.RuneWidth(word[len(word)-1])
			if uniseg.StringWidth(string(word))+lastCharLen > width {
				if len(lines[row]) > 0 {
					row++
					lines = append(lines, []rune{})
				}
				lines[row] = append(lines[row], word...)
				word = nil
			}
		}
	}

	if uniseg.StringWidth(string(lines[row]))+uniseg.StringWidth(string(word))+spaces >= width {
		lines = append(lines, []rune{})
		lines[row+1] = append(lines[row+1], word...)
		spaces++
		lines[row+1] = append(lines[row+1], taSpaces(spaces)...)
	} else {
		lines[row] = append(lines[row], word...)
		spaces++
		lines[row] = append(lines[row], taSpaces(spaces)...)
	}

	return lines
}

func taSpaces(n int) []rune {
	return []rune(strings.Repeat(" ", n))
}
