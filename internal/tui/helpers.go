package tui

import (
	"os/exec"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/aeon022/missionctl-core/ui"
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

// rowMode says how a list row is highlighted.
type rowMode int

const (
	rowNormal rowMode = iota
	rowSelected
	rowHover
)

// styledRow wraps row content in the 2-column gutter and the row highlight:
// selected = accent bar + one continuous full-width background (ui.Row repaints
// the background after every inner color, and lifts dimmed text to Muted so it
// stays readable on it); hover = ui.HoverRow: the hover background WITHOUT
// flattening the row's own colors (dates, tags).
func styledRow(width int, mode rowMode, content string) string {
	switch mode {
	case rowSelected:
		return ui.Row(width, true, content)
	case rowHover:
		return ui.HoverRow(width, content)
	}
	return ui.Row(width, false, content)
}

// shortFolder fits a folder path into budget cells, keeping the END of the
// path (the part that tells notebooks apart): "Change-Management/Howtos/KI"
// -> "…/Howtos/KI" -> "…/KI" -> mid-ellipsis of the last segment.
func shortFolder(folder string, budget int) string {
	if budget <= 0 {
		return ""
	}
	if runewidth.StringWidth(folder) <= budget {
		return folder
	}
	segs := strings.Split(folder, "/")
	for k := 1; k < len(segs); k++ { // drop as few leading segments as possible
		if c := "…/" + strings.Join(segs[k:], "/"); runewidth.StringWidth(c) <= budget {
			return c
		}
	}
	return ui.MidEllipsis(segs[len(segs)-1], budget)
}

// noteRowContent builds the unhighlighted content of a note row in exactly
// width cells: age-colored date, title, then the notebook (dimmed, shortened
// from the left) and first tag. The title gets everything the meta doesn't
// need, and the folder is capped to about a quarter of the row so it can't
// crowd the title out.
func noteRowContent(n *models.Note, width int, query string) string {
	dateStyled := coloredDate(smartDate(n.ModTime), n.ModTime)

	title := n.Title
	if idx := strings.Index(title, "\n"); idx >= 0 {
		title = title[:idx]
	}
	title = strings.TrimSpace(title)

	// Truncation and padding use the same yardstick (runewidth/lipgloss.Width)
	// throughout, deliberately: mixing a conservative truncation measure with a
	// plain pad measure made rows land at different real columns depending on
	// whether a title held an ambiguous-width rune (a jagged two-pane divider).
	meta := ""
	metaBudget := width - 16 - 6 // the title keeps a 6-cell floor
	if n.Folder != "" && metaBudget > 1 {
		// a quarter of the row at least; a short title leaves more room, so the
		// notebook path stays whole where it fits
		cap := min(metaBudget, max(width/4, 14, width-16-runewidth.StringWidth(title)-1))
		folder := " " + shortFolder(n.Folder, cap-1)
		meta += styleMuted.Render(folder)
		metaBudget -= runewidth.StringWidth(folder)
	}
	if len(n.Tags) > 0 && metaBudget > 1 {
		tag := runewidth.Truncate(" #"+n.Tags[0], metaBudget, "…")
		meta += styleTag.Render(tag)
	}
	titleW := max(width-16-lipgloss.Width(meta), 6)

	matchIdx := fuzzyMatchIndexes(query, title)
	titleTrunc := runewidth.Truncate(title, titleW, "…")
	titleStyled := highlightMatches(titleTrunc, matchIdx, lipgloss.NewStyle())
	if pad := titleW - runewidth.StringWidth(titleTrunc); pad > 0 {
		titleStyled += strings.Repeat(" ", pad)
	}

	row := dateStyled + "  " + titleStyled + meta
	if pad := width - lipgloss.Width(row); pad > 0 {
		row += strings.Repeat(" ", pad)
	}
	return row
}

// formatNoteRow builds a note list row of exactly width cells: a 2-column
// gutter (accent bar when selected) plus noteRowContent.
func formatNoteRow(n *models.Note, width int, mode rowMode, query string) string {
	return styledRow(width, mode, noteRowContent(n, max(width-2, 1), query))
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

// emptySuggestion is emptyHint without its "No notes — " lead, for the
// emptystate block that shows "No notes" as its own title.
func emptySuggestion() string { return strings.TrimPrefix(emptyHint(), "No notes — ") }

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

// copyToClipboardCmd copies via OSC 52 (works over SSH/tmux) and pbcopy
// (for terminals that ignore OSC 52, e.g. Terminal.app).
func copyToClipboardCmd(text string) tea.Cmd {
	return tea.Batch(tea.SetClipboard(text), func() tea.Msg {
		cmd := exec.Command("pbcopy")
		cmd.Stdin = strings.NewReader(text)
		if err := cmd.Run(); err != nil {
			return errMsg{err}
		}
		return nil
	})
}

const focusReloadAfter = 5 * time.Second

// browsingIdle reports whether the user is just looking at the note list:
// no detail/editor/settings/help/tags/graph view, search, palette, pending
// delete confirmation, or work in flight.
func (m Model) browsingIdle() bool {
	return m.view == viewList && !m.searching && !m.inPalette &&
		m.confirmID == "" && !m.syncing && !m.loading
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
