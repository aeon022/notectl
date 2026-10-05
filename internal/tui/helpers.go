package tui

import (
	"os/exec"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/aeon022/notectl/internal/config"
	"github.com/aeon022/notectl/internal/models"
	"github.com/aeon022/notectl/internal/notes"
	"github.com/mattn/go-runewidth"
)

// ── Helpers ───────────────────────────────────────────────────────────────────

// splitTitleBlock treats an Apple note's first block as its title (Notes
// derives the displayed title from the body's first line, so the editor
// shows it as its own field rather than duplicating it atop the body text)
// and everything after it as the editable body.
func splitTitleBlock(blocks []notes.Block) (title, rest string) {
	if len(blocks) == 0 {
		return "", ""
	}
	return blocks[0].Plain, notes.BlocksToPlain(blocks[1:])
}

// dropNote removes id from BOTH the visible (filtered) list and allNotes (the
// source search, tags and the link graph work from). Editing only m.notes
// left deleted notes in allNotes: with no filter the in-place append even
// duplicated the last note there, and with a filter the note came back when
// the search cleared.
func (m *Model) dropNote(id string) {
	keep := func(list []models.Note) []models.Note {
		out := make([]models.Note, 0, len(list))
		for _, n := range list {
			if n.ID != id {
				out = append(out, n)
			}
		}
		return out
	}
	m.notes = keep(m.notes)
	m.allNotes = keep(m.allNotes)
}

// setNoteBody updates a cached note's body in both the visible and full list.
func (m *Model) setNoteBody(id, body string) {
	for _, list := range [][]models.Note{m.notes, m.allNotes} {
		for i := range list {
			if list[i].ID == id {
				list[i].Body = body
			}
		}
	}
}

func (m *Model) resetNew(title string) {
	m.bodyArea.SetWidth(m.editorBodyWidth()) // paneRatio may have changed since last resize
	m.editorYOffset = 0
	m.titleInput.SetValue(title)
	m.tagsInput.SetValue("")
	m.bodyArea.SetValue("")
	m.newFocus = 0
	m.titleInput.Focus()
	m.tagsInput.Blur()
	m.bodyArea.Blur()
}

func (m *Model) blurNew(f int) {
	switch f {
	case 0:
		m.titleInput.Blur()
	case 1:
		m.tagsInput.Blur()
	case 2:
		m.bodyArea.Blur()
	}
}

func (m *Model) focusNew(f int) {
	switch f {
	case 0:
		m.titleInput.Focus()
	case 1:
		m.tagsInput.Focus()
	case 2:
		m.bodyArea.Focus()
	}
}

func (m *Model) setStatus(s string) {
	m.status = s
	m.statusTime = time.Now()
}

func (m Model) bodyHeight() int {
	h := m.height - 10
	if h < 5 {
		h = 5
	}
	return h
}

// detailBodyWidth is the wrap width for detail-view body content, leaving
// room for detailLeftPad on the left and the scrollbar glyph on the right
// (see renderDetail/renderScrollbar) so text doesn't run flush to either.
func (m Model) detailBodyWidth() int {
	w := m.width - 4
	if w < 10 {
		w = 10
	}
	return w
}

// detailLeftPad is the left margin applied to every line in the detail
// view (header fields and scrollable body alike).
const detailLeftPad = "  "

// formatNoteRow builds a note list row. rowStyle carries the selected-row
// treatment (background+foreground+bold) and is applied directly to the
// title segment — NOT via an outer Render() wrapping the whole composed
// row. That used to be how this worked (the caller wrapped the return
// value in styleSelected.Width(w).Render(...)), and it was broken the same
// way as an equivalent bug found and fixed in mailctl: dateStyled/meta
// below carry their OWN independent colors, and lipgloss's Render() ends
// every string with a full SGR reset — the first inner segment's reset
// clobbered the outer wrap's style for everything after it, so a selected
// row's highlight background didn't extend past the date column. Fixed by
// applying rowStyle per-segment instead, which also makes it safe to
// highlight fuzzy matches here even on the selected row.
func formatNoteRow(n *models.Note, width int, rowStyle lipgloss.Style, query string) string {
	dateStr := smartDate(n.ModTime)
	dateStyled := coloredDate(dateStr, n.ModTime) // independent color, unaffected by rowStyle

	title := n.Title
	if idx := strings.Index(title, "\n"); idx >= 0 {
		title = title[:idx]
	}
	title = strings.TrimSpace(title)

	// Reserve the title's 6-char floor first, then give folder/tag meta
	// whatever's left, truncating each to fit — the other way around (meta
	// rendered at full length, title floored to 6 regardless of what that
	// left) let a long folder/tag on a narrow terminal push the row past
	// `width` altogether, since nothing here ever shrank meta back down.
	// That overflow broke the two-pane divider's alignment (row + " │ " +
	// preview) once row exceeded its column budget.
	// Truncation and padding both measure with the same yardstick
	// (runewidth/lipgloss.Width) throughout this function, deliberately —
	// an earlier version truncated conservatively (assuming an ambiguous-
	// width rune like a dash might render one column wider than measured,
	// see pessimisticWidth) but then padded against the plain, narrower
	// measurement. That mismatch meant a row's actual on-screen width
	// silently depended on whether its title happened to contain such a
	// rune — most did — so two rows padded to the "same" width landed at
	// different real columns, and the two-pane "│" divider came out jagged
	// instead of a straight line (reported live, and worse in practice
	// than the theoretical overflow the mismatch was trying to prevent).
	// Consistent measurement can't simultaneously guarantee zero overflow
	// on a terminal that renders such a rune wide — but it does guarantee
	// every row lands at the exact same column, which is what actually
	// matters here.
	meta := "" // independent colors (folder/tag), unaffected by rowStyle
	metaBudget := width - 16 - 6
	if n.Folder != "" && metaBudget > 1 {
		folder := runewidth.Truncate(" "+n.Folder, metaBudget, "…")
		meta += styleFolder.Render(folder)
		metaBudget -= runewidth.StringWidth(folder)
	}
	if len(n.Tags) > 0 && metaBudget > 1 {
		tag := runewidth.Truncate(" #"+n.Tags[0], metaBudget, "…")
		meta += styleTag.Render(tag)
	}
	metaW := lipgloss.Width(meta)
	titleW := width - 16 - metaW
	if titleW < 6 {
		titleW = 6
	}

	matchIdx := fuzzyMatchIndexes(query, title)
	titleTrunc := runewidth.Truncate(title, titleW, "…")
	titleStyled := highlightMatches(titleTrunc, matchIdx, rowStyle)
	if pad := titleW - runewidth.StringWidth(titleTrunc); pad > 0 {
		titleStyled += rowStyle.Render(strings.Repeat(" ", pad))
	}

	row := dateStyled + rowStyle.Render("  ") + titleStyled + meta

	// Pad to full width with rowStyle so a selected row's background spans
	// the whole line, not just up to the last character of content.
	if pad := width - lipgloss.Width(row); pad > 0 {
		row += rowStyle.Render(strings.Repeat(" ", pad))
	}
	return row
}

func coloredDate(s string, t time.Time) string {
	now := time.Now()
	// pad to fixed 14-char visual width before coloring
	runes := []rune(s)
	padded := string(runes) + strings.Repeat(" ", 14-len(runes))
	switch {
	case sameDay(t, now):
		return styleDateToday.Render(padded)
	case t.After(now.AddDate(0, 0, -7)):
		return styleDateWeek.Render(padded)
	case t.After(now.AddDate(0, -1, 0)):
		return styleDateMonth.Render(padded)
	default:
		return styleDateOld.Render(padded)
	}
}

func firstBodyLine(body string) string {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		// skip markdown headings and empty lines
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "---") {
			continue
		}
		// strip list markers and quotes for preview
		line = strings.TrimPrefix(line, "- ")
		line = strings.TrimPrefix(line, "* ")
		line = strings.TrimPrefix(line, "> ")
		line = strings.TrimPrefix(line, "[ ] ")
		line = strings.TrimPrefix(line, "[x] ")
		line = strings.TrimPrefix(line, "[X] ")
		return line
	}
	return ""
}

func emptyHint() string {
	switch config.Source() {
	case config.SourceApple:
		return "No notes — press s to sync from Apple Notes"
	case config.SourceObsidian, config.SourceMarkdown:
		return "No notes — press p to set vault path, then s to sync"
	default:
		return "No notes — press p to configure a source, then s to sync"
	}
}

func dateGroup(t time.Time) string {
	now := time.Now()
	switch {
	case sameDay(t, now):
		return "Today"
	case sameDay(t, now.AddDate(0, 0, -1)):
		return "Yesterday"
	case t.After(now.AddDate(0, 0, -7)):
		return t.Format("Monday")
	case t.After(now.AddDate(0, -1, 0)):
		return "This month"
	case t.Year() == now.Year():
		return t.Format("January")
	default:
		return t.Format("January 2006")
	}
}

func renderGroupHeader(group string, width int) string {
	label := " " + group + " "
	dashes := width - len([]rune(label)) - 3
	if dashes < 2 {
		dashes = 2
	}
	return styleMuted.Render("──" + label + strings.Repeat("─", dashes))
}

// applySortOrder sorts m.notes and restores cursor by ID.
func (m Model) applySortOrder() Model {
	if m.sortByDate {
		return m // SQL already returns date-sorted
	}
	var curID string
	if m.cursor < len(m.notes) {
		curID = m.notes[m.cursor].ID
	}
	sort.Slice(m.notes, func(i, j int) bool {
		return strings.ToLower(m.notes[i].Title) < strings.ToLower(m.notes[j].Title)
	})
	for i, n := range m.notes {
		if n.ID == curID {
			m.cursor = i
			break
		}
	}
	return m
}

func copyToClipboardCmd(text string) tea.Cmd {
	return func() tea.Msg {
		cmd := exec.Command("pbcopy")
		cmd.Stdin = strings.NewReader(text)
		if err := cmd.Run(); err != nil {
			return errMsg{err}
		}
		return nil
	}
}

func smartDate(t time.Time) string {
	now := time.Now()
	switch {
	case sameDay(t, now):
		return t.Format("      15:04")
	case t.After(now.AddDate(0, 0, -6)):
		return t.Format("Mon   15:04")
	case t.Year() == now.Year():
		return t.Format("Jan 02 15:04")
	default:
		return t.Format("Jan 02  2006")
	}
}

func sameDay(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}
