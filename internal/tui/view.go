package tui

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/aeon022/missionctl-core/emptystate"
	"github.com/aeon022/missionctl-core/keymap"
	"github.com/aeon022/missionctl-core/overlay"
	"github.com/aeon022/missionctl-core/palette"
	"github.com/aeon022/missionctl-core/statusbar"
	"github.com/aeon022/missionctl-core/theme"
	"github.com/aeon022/missionctl-core/ui"
	"github.com/aeon022/notectl/internal/config"
	"github.com/aeon022/notectl/internal/models"
	"github.com/mattn/go-runewidth"
)

// ── View ──────────────────────────────────────────────────────────────────────

func (m Model) View() tea.View {
	v := tea.NewView(m.viewContent())
	// v1's tea.WithAltScreen()/WithMouseAllMotion() Program options are
	// gone in v2 — AltScreen/MouseMode are now per-View fields, set on
	// every render instead of once at Program startup.
	v.AltScreen = true
	v.MouseMode = tea.MouseModeAllMotion
	v.ReportFocus = true // FocusMsg → reload the list when the window regains focus
	return v
}

func (m Model) viewContent() string {
	switch m.view {
	case viewDetail:
		return m.renderDetail()
	case viewNew:
		return m.renderNew()
	case viewSettings:
		return m.renderSettings()
	case viewHelp:
		// "?" is only reachable from the main list, so the list is always
		// the correct background to keep visible behind the popup. No
		// enclosing border on the list view, so inset 0 is safe.
		return overlay.CenterDim(m.renderList(), m.renderHelpPopup(), m.width, m.height, 0)
	case viewTags:
		return overlay.CenterDim(m.renderList(), m.renderTags(), m.width, m.height, 0)
	case viewGraph:
		return m.renderGraph()
	default:
		return m.renderList()
	}
}

func (m Model) renderTags() string {
	tags := allTags(m.allNotes)
	w := min(50, m.width-4)
	var rows []string
	if len(tags) == 0 {
		rows = append(rows, styleHelp.Render("No tagged notes yet."))
	}
	// window the list around the cursor so a long tag list never outgrows the screen
	room := max(3, m.height-8)
	start := 0
	if m.tagCursor >= room {
		start = m.tagCursor - room + 1
	}
	for i := start; i < len(tags) && i < start+room; i++ {
		row := fmt.Sprintf("#%s  (%d)", tags[i].name, tags[i].count)
		if i == m.tagCursor {
			rows = append(rows, styleSelected.Render("› "+row))
		} else {
			rows = append(rows, "  "+row)
		}
	}
	rows = append(rows, "", statusbar.Hints(w-4, [2]string{"esc", "close"}, [2]string{"j/k", "move"}, [2]string{"enter", "filter"}))
	for i := range rows {
		rows[i] = " " + rows[i]
	}
	return ui.Panel(w, len(rows)+2, "Tags", strings.Join(rows, "\n"), true)
}

// renderGraph draws the link-graph explorer: the focus note centered, its
// outgoing wiki-links above and incoming backlinks below, connected with
// simple box-drawing lines. Not a force-directed layout — a terminal has no
// room for that — just a one-hop neighbor view you can walk through.
func (m Model) renderGraph() string {
	if m.graphFocus == nil {
		return ""
	}
	var b strings.Builder
	cursor := 0
	renderNeighbor := func(n models.Note, arrow string) {
		row := arrow + " " + n.Title
		if cursor == m.graphCursor {
			b.WriteString("  " + styleSelected.Render("› "+row) + "\n")
		} else {
			b.WriteString("    " + styleMuted.Render(row) + "\n")
		}
		cursor++
	}

	if len(m.graphOut) == 0 {
		b.WriteString("    " + styleHelp.Render("(no outgoing links)") + "\n")
	}
	for _, n := range m.graphOut {
		renderNeighbor(n, "──▶")
	}

	b.WriteString("\n  " + styleTag.Render("┃ "+m.graphFocus.Title) + "\n\n")

	if len(m.graphIn) == 0 {
		b.WriteString("    " + styleHelp.Render("(no backlinks)") + "\n")
	}
	for _, n := range m.graphIn {
		renderNeighbor(n, "◀──")
	}

	return m.secondary("Link Graph", m.graphFocus.Title, b.String(), "",
		[2]string{"esc", "back"}, [2]string{"j/k", "move"}, [2]string{"enter", "re-focus"}, [2]string{"d", "open note"})
}

func (m Model) helpContent() string {
	return keymap.New("notectl", "notes from the terminal").
		Section("Navigation").
		Row("j / k", "move down / up").
		Row("g / G", "jump to top / bottom").
		Row("pgdn/up", "page down / up").
		Row("tab", "next notebook (steps into an expanded one's children, then the next)").
		Row("s-tab", "previous notebook").
		Row("l / right", "expand notebook (▸→▾), or step into its first child if already expanded").
		Row("h / left", "collapse notebook, or step up from a child to its parent").
		Row("[ / ]", "previous / next account (only when Apple Notes has more than one)").
		Row("< / >", "resize panes (two-pane layout)").
		Row(":", "command palette — type an action by name").
		Section("Notes").
		Row("enter", "open note").
		Row("n", "new note").
		Row("e", "edit note").
		Row("d", "delete note (asks to confirm)").
		Row("o", "open in external app").
		Row("y", "copy title to clipboard").
		Section("Other").
		Row("S", "toggle sort (date / title A–Z)").
		Row("H", "toggle hiding empty notebooks").
		Row("p", "settings (vault path, source)").
		Row("s", "sync").
		Row("/", "search (esc clears)").
		Row("t", "browse tags").
		Row("L", "link graph (browse [[wiki-links]])").
		Row("?", "toggle this help").
		Row("q", "quit").
		String()
}

// openHelp sizes and populates the transient help popup (see
// renderHelpPopup/overlay.Center) from the ACTUAL rendered background
// height, not the terminal size.
func (m Model) openHelp() Model {
	bgLines := strings.Split(m.renderList(), "\n")

	safeH := max(6, len(bgLines))
	popH := min(safeH, 24)
	popW := min(70, m.width)
	if popW < 40 {
		popW = 40
	}

	vp := viewport.New(viewport.WithWidth(popW-4), viewport.WithHeight(popH-3)) // ui.Panel border → 2 rows/cols, 1-col side margin, -1 row for footer
	vp.SetContent(m.helpContent())

	m.helpVP = vp
	m.helpPopW = popW
	m.helpPopH = popH
	m.view = viewHelp
	return m
}

// renderHelpPopup renders the help viewport in a bordered box, meant to be
// composited over the list view via overlay.Center rather than replacing
// the whole screen — the list stays visible around it.
func (m Model) renderHelpPopup() string {
	footer := "esc / ?  close"
	if m.helpVP.TotalLineCount() > m.helpVP.Height() {
		footer = fmt.Sprintf("j/k scroll (%d%%)  ·  %s", int(m.helpVP.ScrollPercent()*100), footer)
	}
	lines := append(strings.Split(m.helpVP.View(), "\n"), styleHelp.Render(footer))
	for i := range lines {
		lines[i] = " " + lines[i]
	}
	return ui.Panel(m.helpPopW, m.helpPopH, "Help", strings.Join(lines, "\n"), true)
}

func (m Model) renderList() string {
	if m.isTwoPane() {
		return m.renderTwoPane()
	}
	return m.renderSinglePane()
}

// renderPaletteBlock renders the ":" command palette's input line + up to 6
// live-filtered matches. listHeight/listStartY reserve exactly as many
// lines as this can produce (1 input + up to 6 matches + 1 blank), so the
// list below never overflows the terminal and pushes this block itself off
// screen.
func (m Model) renderPaletteBlock() string {
	var b strings.Builder
	b.WriteString("  " + m.paletteInput.View() + "\n")
	matches := palette.Match(paletteCommands, m.paletteInput.Value())
	if len(matches) > 6 {
		matches = matches[:6]
	}
	if len(matches) == 0 {
		b.WriteString("    " + styleMuted.Render("no matching command") + "\n")
	}
	for i, c := range matches {
		row := fmt.Sprintf("%-9s %s", c.Name, c.Desc)
		if i == m.paletteCursor {
			b.WriteString("    " + styleSelected.Render("▶ "+row) + "\n")
		} else {
			b.WriteString("      " + styleMuted.Render(row) + "\n")
		}
	}
	b.WriteString("\n")
	return b.String()
}

// ── Single-pane (narrow terminals) ────────────────────────────────────────────

func (m Model) renderSinglePane() string {
	var b strings.Builder
	if m.searching {
		b.WriteString("  " + m.searchInput.View() + "\n\n")
	}
	if m.searchQ != "" {
		b.WriteString(styleMuted.Render("  /"+m.searchQ) + "\n")
	}
	if m.inPalette {
		b.WriteString(m.renderPaletteBlock())
	}

	listH := m.listHeight()

	if m.loading {
		b.WriteString(emptystate.Loading(m.width, listH, m.sp.View(), "Loading notes…") + "\n")
	} else if len(m.notes) == 0 {
		b.WriteString(emptystate.Render(m.width, listH, "📝", "No notes", emptySuggestion()) + "\n")
	} else {
		lines, cursorLine := m.buildListLines(m.width, true)
		start := 0
		if cursorLine >= listH {
			start = cursorLine - listH + 1
		}
		end := min(len(lines), start+listH)
		for _, l := range lines[start:end] {
			b.WriteString(l + "\n")
		}
		for i := end - start; i < listH; i++ {
			b.WriteString("\n")
		}
	}

	return m.frame(b.String())
}

// frame stacks the header chrome, the body and the footer into exactly
// m.height lines (ui.Frame), so a shorter or taller body can never leave stale
// lines behind or push the footer off screen.
func (m Model) frame(body string) string {
	return ui.Frame(m.height, m.renderChrome(), strings.TrimSuffix(body, "\n"), "\n "+m.renderHelpBar(m.width-1)) // 1-column margin like the header and tabs
}

// renderChrome draws the lines above the list: see the tier table in tabs.go.
func (m Model) renderChrome() string {
	w := m.width - 1
	hdr := " " + m.renderAppHeader(w)
	tabs := " " + m.renderTabRow1(w)
	var folders []string
	if row := m.renderTabRow2(w); row != "" {
		folders = []string{" " + row}
	}
	div := styleDivider.Render(strings.Repeat("─", m.width))
	var lines []string
	if m.spacious() {
		lines = append([]string{hdr, div, "", tabs}, folders...)
		lines = append(lines, "")
	} else {
		lines = append([]string{hdr, tabs}, folders...)
		lines = append(lines, div)
	}
	return strings.Join(lines, "\n")
}

// ── Two-pane (wide terminals) ─────────────────────────────────────────────────

func (m Model) renderTwoPane() string {
	leftW := m.leftWidth()
	rightW := m.pvpWidth()
	paneH := m.listHeight()

	var b strings.Builder

	// search row replaces one line of the pane
	if m.searching {
		b.WriteString("  " + m.searchInput.View() + "\n")
	}
	if m.inPalette {
		b.WriteString(m.renderPaletteBlock())
	}

	// Reserve a 1-column margin on each side of the divider (left pane's
	// own left edge, both sides of the "│", and the right pane's right
	// edge implicitly via its narrower content width) so list rows and
	// preview text don't render flush against the pane borders.
	const pad = 1
	listContentW := max(1, leftW-pad*2)
	rightContentW := max(1, rightW-pad)

	// ── left: note list ──
	var leftLines []string
	if m.loading {
		leftLines = strings.Split(emptystate.Loading(listContentW, paneH, m.sp.View(), "Loading notes…"), "\n")
	} else if len(m.notes) == 0 {
		leftLines = strings.Split(emptystate.Render(listContentW, paneH, "📝", "No notes", emptySuggestion()), "\n")
	} else {
		lines, cursorLine := m.buildListLines(listContentW, false)
		start := 0
		if cursorLine >= paneH {
			start = cursorLine - paneH + 1
		}
		end := min(len(lines), start+paneH)
		leftLines = lines[start:end]
	}

	// ── right: markdown preview ──
	var rightLines []string
	if len(m.notes) > 0 {
		rendered := renderMarkdown(m.notes[m.cursor].Body, rightContentW)
		rightLines = strings.Split(rendered, "\n")
	}

	// combine side by side
	div := styleDivider.Render("│")
	for i := 0; i < paneH; i++ {
		l := ""
		if i < len(leftLines) {
			l = leftLines[i]
		}
		r := ""
		if i < len(rightLines) {
			r = rightLines[i]
		}
		lW := lipgloss.Width(l)
		if lW < listContentW {
			l += strings.Repeat(" ", listContentW-lW)
		}
		b.WriteString(" " + l + " " + div + " " + r + "\n")
	}

	return m.frame(b.String())
}

// buildListLines pre-renders list rows with optional date group headers and preview lines.
func (m Model) buildListLines(w int, withPreview bool) ([]string, int) {
	lines, cursorLine, _ := m.buildListLinesWithMapping(w, withPreview)
	return lines, cursorLine
}

// buildListLinesWithMapping is buildListLines plus a parallel lineToNote
// slice (note index for a main-row or preview line, -1 for a group-header
// or blank-separator line), so rowHitTest can map a clicked screen line
// back to a note without re-deriving this layout itself.
func (m Model) buildListLinesWithMapping(w int, withPreview bool) ([]string, int, []int) {
	var lines []string
	var lineToNote []int
	cursorLine := 0
	lastGroup := ""

	for i := range m.notes {
		n := &m.notes[i]

		// date group header (only when sorted by date)
		if m.sortByDate {
			g := dateGroup(n.ModTime)
			if g != lastGroup {
				if len(lines) > 0 {
					lines = append(lines, "") // blank separator
					lineToNote = append(lineToNote, -1)
				}
				lines = append(lines, renderGroupHeader(g, w))
				lineToNote = append(lineToNote, -1)
				lastGroup = g
			}
		}

		if i == m.cursor {
			cursorLine = len(lines)
		}
		mode := rowNormal
		switch {
		case i == m.cursor:
			mode = rowSelected
		case i == m.hoverRow:
			mode = rowHover
		}
		lines = append(lines, formatNoteRow(n, w, mode, m.searchQ))
		lineToNote = append(lineToNote, i)

		if withPreview && n.Body != "" {
			preview := firstBodyLine(n.Body)
			if preview != "" {
				avail := w - 2 - 16 // 2: the row gutter
				if avail > 10 {
					preview = runewidth.Truncate(preview, avail, "…")
				}
				pLine := strings.Repeat(" ", 16) + styleMuted.Render(preview)
				lines = append(lines, styledRow(w, mode, pLine))
				lineToNote = append(lineToNote, i)
			}
		}
	}
	return lines, cursorLine, lineToNote
}

// preambleRows returns the fixed-height chrome above the note list (see the
// tier table in tabs.go: 5-6 lines spacious, 3-4 compact).
func (m Model) preambleRows() int { return m.chrome().rows }

// listStartY returns the number of preamble lines above the note list —
// header, tab bar(s), divider, and (mode-dependent) an optional search
// input/filter chip — shared by the render paths and rowHitTest so they
// can't drift apart. Two-pane's search row costs 1 line (no trailing
// blank, no separate filter chip line); single-pane's costs 2 plus an
// optional filter chip line.
func (m Model) listStartY() int {
	y := m.preambleRows()
	if m.isTwoPane() {
		if m.searching {
			y++
		}
		if m.inPalette {
			y += 8
		}
		return y
	}
	if m.searching {
		y += 2
	}
	if m.searchQ != "" {
		y++
	}
	if m.inPalette {
		y += 8
	}
	return y
}

// listHeight returns the available line budget for the note list itself,
// matching listH (single-pane) / paneH (two-pane) in the render paths.
func (m Model) listHeight() int {
	if m.isTwoPane() {
		h := m.height - m.preambleRows() - helpBarHeight - 1 // -1: blank padding line above the help bar
		if m.searching {
			h--
		}
		if m.inPalette {
			h -= 8
		}
		if h < 1 {
			h = 1
		}
		return h
	}
	h := m.height - m.listStartY() - helpBarHeight - 1 // -1: blank padding line above the help bar
	if h < 1 {
		h = 1
	}
	return h
}

// rowHitTest returns the m.notes index at screen position (x, y), or -1
// if the click missed. Mirrors buildListLinesWithMapping's line layout,
// listStartY/listHeight's preamble+budget accounting, and each render
// mode's scroll window (start := cursorLine - listHeight + 1) so a click
// lands on the note it visually appears to be over. In two-pane mode, x
// must land inside the left (list) pane, not the preview pane.
func (m Model) rowHitTest(x, y int) int {
	if m.isTwoPane() && x >= m.leftWidth() {
		return -1
	}
	idx := y - m.listStartY()
	if idx < 0 || len(m.notes) == 0 {
		return -1
	}
	w := m.width
	withPreview := true
	if m.isTwoPane() {
		const pad = 1
		w = max(1, m.leftWidth()-pad*2)
		withPreview = false
	}
	_, cursorLine, lineToNote := m.buildListLinesWithMapping(w, withPreview)
	listH := m.listHeight()
	start := 0
	if cursorLine >= listH {
		start = cursorLine - listH + 1
	}
	lineIdx := start + idx
	if lineIdx >= len(lineToNote) {
		return -1
	}
	return lineToNote[lineIdx]
}

// renderAppHeader is "notectl", the account context (see headerContext) in the
// middle and the date on the right, via ui.Header: the context is dropped
// first, then the name truncated, so a long account name never overflows.
func (m Model) renderAppHeader(w int) string {
	return ui.Header(w, styleHeader.Render("notectl"), styleTabParentRef.Render(m.headerContext()),
		styleMuted.Render(time.Now().Format("Mon 02 Jan")))
}

// renderHelpBar renders the one-line footer: key hints in priority order
// (the last ones drop first on a narrow terminal — `?` and `q` sit near the
// front so they survive longest; the full list lives under `?`), with the
// cursor position and sort flush right via statusbar.Line, which truncates
// the hints and never the status or the width — an overflowing bottom row
// would make the terminal scroll and drag everything above it out of
// alignment (reported live as tab-row duplication after a notebook switch).
// An error/status message replaces the hints on the same single line.
func (m Model) renderHelpBar(w int) string {
	right := ""
	if len(m.notes) > 0 {
		sortIcon := "↓date"
		if !m.sortByDate {
			sortIcon = "↓A-Z"
		}
		right = styleHelp.Render(fmt.Sprintf("%d/%d  %s", m.cursor+1, len(m.notes), sortIcon))
	}
	if sync := m.syncStatus(); sync != "" { // sync age sits left of the counter
		if right != "" {
			right = sync + "   " + right
		} else {
			right = sync
		}
	}
	if m.err != nil {
		return statusbar.Line(w, styleErr.Render("✗ "+m.err.Error()), "")
	}
	if m.status != "" {
		if m.confirmID != "" {
			return statusbar.Line(w, styleSyncing.Render("⚠ "+m.status), "")
		}
		return statusbar.Line(w, styleOK.Render("✓ "+m.status), right)
	}
	hints := statusbar.Hints(w-lipgloss.Width(right)-2,
		[2]string{"enter", "open"}, [2]string{"n", "new"}, [2]string{"?", "help"}, [2]string{"q", "quit"},
		[2]string{"/", "search"}, [2]string{"s", "sync"}, [2]string{"e", "edit"}, [2]string{"d", "delete"},
		[2]string{"u", "undo"}, [2]string{"y", "copy"}, [2]string{"tab", "notebook"}, [2]string{"S", "sort"},
		[2]string{"H", "hide empty"}, [2]string{"o", "editor"}, [2]string{"p", "settings"}, [2]string{"l/h", "expand"})
	return statusbar.Line(w, hints, right)
}

// helpBarHeight is the line budget reserved below the list for
// renderHelpBar's output, shared with listHeight so the two can't drift.
const helpBarHeight = 1

// doubleClickWindow opens the note detail on a second click within this
// window, same pattern and duration taskctl uses for its own double-click.
const doubleClickWindow = 400 * time.Millisecond

// undoWindow is how long after a delete "u" still restores it — same
// duration taskctl uses for its own delete-undo.
const undoWindow = 5 * time.Second

func (m Model) renderDetail() string {
	if m.detail == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString(detailLeftPad + styleBold.Render(m.detail.Title) + "\n")
	meta := ""
	if m.detail.Folder != "" {
		meta += styleFolder.Render(m.detail.Folder) + "  "
	}
	for _, t := range m.detail.Tags {
		meta += styleTag.Render("#"+t) + " "
	}
	if meta != "" {
		b.WriteString(detailLeftPad + meta + "\n")
	}
	b.WriteString(detailLeftPad + styleMuted.Render(m.detail.ModTime.Format("Mon, 02 Jan 2006 15:04")) + "\n")
	if backlinks := backlinksFor(*m.detail, m.allNotes); len(backlinks) > 0 {
		names := make([]string, len(backlinks))
		for i, n := range backlinks {
			names[i] = n.Title
		}
		b.WriteString(detailLeftPad + styleMuted.Render("Linked from: ") + styleTag.Render(strings.Join(names, ", ")) + "\n")
	}
	b.WriteString("\n")
	m.vp.SetWidth(m.detailBodyWidth())
	m.vp.SetHeight(m.bodyHeight())
	b.WriteString(renderScrollbar(m.vp, detailLeftPad))
	pct := ""
	if m.vp.TotalLineCount() > m.vp.Height() {
		pct = fmt.Sprintf("%d%%", int(m.vp.ScrollPercent()*100))
	}
	return m.secondary("Note", m.headerContext(), b.String(), styleMuted.Render(pct),
		[2]string{"esc", "back"}, [2]string{"e", "edit"}, [2]string{"q", "quit"}, [2]string{"d", "delete"},
		[2]string{"o", "notes"}, [2]string{"L", "link graph"}, [2]string{"j/k", "scroll"}, [2]string{"space", "toggle checkbox"})
}

// backlinksFor returns every note in all whose body references target via
// an Obsidian-style [[Title]] wiki-link, excluding target itself.
func backlinksFor(target models.Note, all []models.Note) []models.Note {
	needle := "[[" + target.Title + "]]"
	var out []models.Note
	for _, n := range all {
		if n.ID == target.ID {
			continue
		}
		if strings.Contains(n.Body, needle) {
			out = append(out, n)
		}
	}
	return out
}

var wikiLinkRe = regexp.MustCompile(`\[\[([^\]]+)\]\]`)

// outgoingLinksFor returns every note in all that source's body links to via
// an Obsidian-style [[Title]] wiki-link. Titles with no matching note (a
// link to a not-yet-created note) are silently skipped.
func outgoingLinksFor(source models.Note, all []models.Note) []models.Note {
	var out []models.Note
	for _, m := range wikiLinkRe.FindAllStringSubmatch(source.Body, -1) {
		title := m[1]
		for _, n := range all {
			if n.ID != source.ID && n.Title == title {
				out = append(out, n)
				break
			}
		}
	}
	return out
}

// setGraphFocus re-centers the link graph on n, computing its one-hop
// neighbors fresh (m.allNotes is the unfiltered set, so the graph isn't
// limited by the current folder tab or search query).
func (m Model) setGraphFocus(n models.Note) Model {
	m.graphFocus = &n
	m.graphOut = outgoingLinksFor(n, m.allNotes)
	m.graphIn = backlinksFor(n, m.allNotes)
	m.graphCursor = 0
	return m
}

// graphNeighbors is the combined, cursor-addressable neighbor list: outgoing
// links first, then incoming (matches render order).
func (m Model) graphNeighbors() []models.Note {
	out := make([]models.Note, 0, len(m.graphOut)+len(m.graphIn))
	out = append(out, m.graphOut...)
	out = append(out, m.graphIn...)
	return out
}

func renderScrollbar(vp viewport.Model, leftPad string) string {
	content := vp.View()
	lines := strings.Split(content, "\n")
	h := vp.Height()
	if h <= 0 {
		h = len(lines)
	}
	total := vp.TotalLineCount()
	if total <= h {
		var sb strings.Builder
		for _, l := range lines {
			sb.WriteString(leftPad + l + "\n")
		}
		return strings.TrimRight(sb.String(), "\n")
	}
	thumbH := max(1, h*h/total)
	thumbTop := int(vp.ScrollPercent() * float64(h-thumbH))
	track := styleDivider.Render("│")
	// A heavy line rather than a full block ("█") — same single-column
	// width as the track, just a bolder stroke, so the thumb reads as a
	// slim scroll indicator instead of a chunky rectangle bulging out of
	// an otherwise thin line.
	thumb := lipgloss.NewStyle().Foreground(colorBlue).Render("┃")
	var glyphs strings.Builder
	for i := range lines {
		if i > 0 {
			glyphs.WriteByte('\n')
		}
		switch {
		case i == 0:
			// The first visible row is a note's title line, which renders
			// with a full-width selected/highlighted background when the
			// cursor starts there — a bare track glyph butted up against
			// that fill looked like a break in the bar rather than part of
			// it. Leaving it blank lets the bar visually start clean on
			// the row below instead.
			glyphs.WriteByte(' ')
		case i >= thumbTop && i < thumbTop+thumbH:
			glyphs.WriteString(thumb)
		default:
			glyphs.WriteString(track)
		}
	}
	// Content lines are only as wide as their own wrapped text (viewport
	// content isn't right-padded), so appending the glyph column after a
	// manually-padded string was fragile: it needs a width measurement that
	// exactly matches how each line was wrapped, and at least one real
	// emoji in practice ("🛏️", bed + variation selector) gets measured
	// differently by different width functions, throwing just that line's
	// glyph out of column. JoinHorizontal sidesteps the whole problem: it
	// pads the left block to a uniform width using its own single,
	// consistent measurement before attaching the right block, so the
	// glyph column can't drift regardless of what any individual line
	// contains.
	body := lipgloss.JoinHorizontal(lipgloss.Top, content, " "+glyphs.String())
	var sb strings.Builder
	for _, l := range strings.Split(body, "\n") {
		sb.WriteString(leftPad + l + "\n")
	}
	return strings.TrimRight(sb.String(), "\n")
}

func (m Model) renderNew() string {
	name, ctx := "New Note", ""
	if m.editNote != nil {
		name, ctx = "Edit Note", m.editNote.Title
	}
	leftW := m.editorLeftWidth()

	var b strings.Builder
	focus := func(i int) string {
		if m.newFocus == i {
			return styleTabActive.Render("›")
		}
		return "  "
	}

	b.WriteString(focus(0) + " " + styleLabel.Render("Title:") + "  " + m.titleInput.View() + "\n")
	b.WriteString(focus(1) + " " + styleLabel.Render("Tags:") + "   " + m.tagsInput.View() + "\n\n")
	b.WriteString(focus(2) + " " + styleLabel.Render("Body:") + "\n")
	b.WriteString(m.bodyArea.View() + "\n")
	b.WriteString(styleMuted.Render("  # heading  - list  - [ ] checklist  **bold**  *italic*  ~~strike~~  `code`"))
	body := b.String()

	if m.isTwoPane() {
		// ── live preview pane (wide terminals) ──
		rightW := m.editorPvpWidth()
		rightLines := []string{styleMuted.Render(" Preview"), ""}
		rightLines = append(rightLines, strings.Split(renderMarkdown(m.bodyArea.Value(), rightW-1), "\n")...)
		leftLines := strings.Split(body, "\n")
		div := styleDivider.Render("│")
		rows := max(len(leftLines), len(rightLines))
		var out []string
		for i := 0; i < rows; i++ {
			l := ""
			if i < len(leftLines) {
				l = leftLines[i]
			}
			r := ""
			if i < len(rightLines) {
				r = " " + rightLines[i]
			}
			if lW := lipgloss.Width(l); lW < leftW {
				l += strings.Repeat(" ", leftW-lW)
			}
			out = append(out, l+div+r)
		}
		body = strings.Join(out, "\n")
	}
	return m.secondary(name, ctx, body, "",
		[2]string{"esc", "cancel"}, [2]string{"ctrl+s", "save"}, [2]string{"tab", "next field"})
}

func (m Model) renderSettings() string {
	var b strings.Builder

	// Vault path only means anything for Obsidian/Markdown — showing it for
	// Apple or Joplin was misleading (it looked editable/relevant when it
	// silently did nothing for either source).
	selectedSource := sourceTypes[m.sourceIdx].key
	if selectedSource == config.SourceObsidian || selectedSource == config.SourceMarkdown {
		b.WriteString(styleLabel.Render("Vault:") + "\n")
		b.WriteString("  " + m.vaultInput.View() + "\n")
		if strings.HasPrefix(m.vaultInput.Value(), "~") {
			resolved := config.VaultPath()
			if _, err := filepath.Abs(resolved); err == nil {
				b.WriteString(styleMuted.Render("  → "+resolved) + "\n")
			}
		}
		b.WriteString("\n")
	} else if selectedSource == config.SourceJoplin {
		b.WriteString(styleLabel.Render("Joplin:") + "\n")
		tokenStatus := "not set — see notectl.yaml (joplin_token) or NOTECTL_JOPLIN_TOKEN"
		if config.JoplinToken() != "" {
			tokenStatus = "configured"
		}
		b.WriteString("  " + styleMuted.Render(config.JoplinAPIURL()+" — token: "+tokenStatus) + "\n\n")
	}

	b.WriteString(styleLabel.Render("Source:") + "\n  ")
	for i, s := range sourceTypes {
		if i == m.sourceIdx {
			b.WriteString(styleTabActive.Render(s.label))
		} else {
			b.WriteString(styleTabInact.Render(s.label))
		}
		if i < len(sourceTypes)-1 {
			b.WriteString("  ")
		}
	}
	b.WriteString("\n")
	b.WriteString("  " + styleMuted.Render(sourceTypes[m.sourceIdx].note) + "\n\n")

	if m.err != nil {
		b.WriteString(styleErr.Render("✗ "+m.err.Error()) + "\n")
	} else if m.status != "" {
		b.WriteString(styleOK.Render("✓ "+m.status) + "\n")
	}
	return m.secondary("Settings", "", b.String(), "",
		[2]string{"esc", "cancel"}, [2]string{"ctrl+s", "save"}, [2]string{"←/→", "source"})
}

// ── Markdown renderer ─────────────────────────────────────────────────────────

func renderMarkdown(body string, width int) string {
	if body == "" {
		return styleMuted.Render("(empty)")
	}
	lines := strings.Split(body, "\n")
	lines = preprocessMarkdownTables(lines, width)
	states := codeStates(lines)
	var sb strings.Builder
	prevBlank := true
	for i, line := range lines {
		if states[i] != notCode {
			sb.WriteString(renderCodeBlockLine(states[i], line, width) + "\n")
			prevBlank = false
			continue
		}
		if isMDHeading(line) && !prevBlank {
			sb.WriteString("\n") // headings are set apart from the text above
		}
		sb.WriteString(renderMDLine(line, width) + "\n")
		prevBlank = strings.TrimSpace(line) == ""
	}
	return lipgloss.NewStyle().Width(width).Render(sb.String())
}

func isMDHeading(line string) bool {
	t := strings.TrimSpace(line)
	return strings.HasPrefix(t, "# ") || strings.HasPrefix(t, "## ") || strings.HasPrefix(t, "### ")
}

// codeState says whether a line is outside code, a ``` fence marker, or inside
// a fenced block.
type codeState int

const (
	notCode codeState = iota
	fenceLine
	codeLine
)

// codeStates marks every line of a note. Fences toggle: an unterminated block
// runs to the end (as most renderers do), and a line with two fences on it
// ("```code```") is an ordinary inline span, not a fence. Without this state,
// code lines were rendered as markdown — a "# comment" became a heading — and
// the fences themselves showed up as literal backticks.
func codeStates(lines []string) []codeState {
	st := make([]codeState, len(lines))
	in := false
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "```") && strings.Count(t, "```") < 2 {
			st[i] = fenceLine
			in = !in
			continue
		}
		if in {
			st[i] = codeLine
		}
	}
	return st
}

// styleMDCodeBlock paints code lines on a distinct surface (the suite's hover
// background) in the code color.
var styleMDCodeBlock = styleMDCode.Background(theme.HoverBgV2)

// renderCodeBlockLine draws one line of a fenced block, padded to width so the
// block reads as a box: fence markers become blank edge rows (the opening one
// carries the language, dimmed), code lines are shown verbatim — no inline
// markdown — with tabs expanded.
func renderCodeBlockLine(st codeState, line string, width int) string {
	pad := func(text string, st lipgloss.Style) string {
		return st.Render(text + strings.Repeat(" ", max(width-runewidth.StringWidth(text), 0)))
	}
	if st == fenceLine {
		lang := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "```"))
		if lang == "" {
			return pad("", styleMDCodeBlock)
		}
		return pad("  "+lang, styleMuted.Background(theme.HoverBgV2))
	}
	return pad("  "+strings.ReplaceAll(line, "\t", "    "), styleMDCodeBlock)
}

func renderMDLine(line string, width int) string {
	t := strings.TrimSpace(line)
	idx := strings.IndexFunc(line, func(r rune) bool { return r != ' ' && r != '\t' })
	leading := ""
	if idx > 0 {
		leading = line[:idx]
	}

	switch {
	case strings.HasPrefix(t, "### "):
		return leading + styleMDH3.Render(strings.TrimPrefix(t, "### "))
	case strings.HasPrefix(t, "## "):
		return leading + styleMDH2.Render(strings.TrimPrefix(t, "## "))
	case strings.HasPrefix(t, "# "):
		return leading + styleMDH1.Render(strings.TrimPrefix(t, "# "))
	case strings.HasPrefix(t, "> "):
		return leading + styleMDQuote.Render("│ "+renderInline(strings.TrimPrefix(t, "> ")))
	case t == ">":
		return leading + styleMDQuote.Render("│")
	case t == "---" || t == "***" || t == "___":
		return styleDivider.Render(strings.Repeat("─", width))
	case strings.HasPrefix(t, "├"):
		return leading + styleMuted.Render(t)
	case strings.HasPrefix(t, "│"):
		var sb strings.Builder
		sb.WriteString(leading)
		parts := strings.Split(t, "│")
		for j, p := range parts {
			if j > 0 {
				sb.WriteString(styleMuted.Render("│"))
			}
			sb.WriteString(renderInline(p))
		}
		return sb.String()
	case strings.HasPrefix(t, "- [ ] ") || strings.HasPrefix(t, "* [ ] "):
		return leading + styleMuted.Render("☐ ") + renderInline(t[6:])
	case strings.HasPrefix(t, "- [x] ") || strings.HasPrefix(t, "- [X] ") ||
		strings.HasPrefix(t, "* [x] ") || strings.HasPrefix(t, "* [X] "):
		return leading + styleStrike.Render("☑ "+renderInline(t[6:]))
	case config.Source() == config.SourceApple && (strings.HasPrefix(t, "• ") || strings.HasPrefix(t, "- ") || strings.HasPrefix(t, "* ")):
		text := t
		if strings.HasPrefix(t, "• ") {
			text = strings.TrimPrefix(t, "• ")
		} else {
			text = t[2:]
		}
		if isItem, done := checklistLookup(text); isItem {
			if done {
				return leading + styleStrike.Render("☑ "+renderInline(text))
			}
			return leading + styleMuted.Render("☐ ") + renderInline(text)
		}
		return leading + "  • " + renderInline(text)
	case strings.HasPrefix(t, "- ") || strings.HasPrefix(t, "* "):
		return leading + "  • " + renderInline(t[2:])
	case strings.HasPrefix(t, "• "):
		return leading + "  " + renderInline(t)
	case strings.HasPrefix(t, "☑ "):
		return leading + styleStrike.Render("☑ "+renderInline(strings.TrimPrefix(t, "☑ ")))
	case strings.HasPrefix(t, "☐ "):
		return leading + styleMuted.Render("☐ ") + renderInline(strings.TrimPrefix(t, "☐ "))
	case strings.HasPrefix(t, "```"):
		return styleMDCode.Render(t)
	default:
		return renderInline(line)
	}
}

func renderInline(s string) string {
	var out strings.Builder
	for i := 0; i < len(s); {
		// **bold**
		if strings.HasPrefix(s[i:], "**") {
			if end := strings.Index(s[i+2:], "**"); end >= 0 {
				leading, content, trailing := splitLeadingTrailingSpaces(s[i+2 : i+2+end])
				out.WriteString(leading + styleMDBold.Render(content) + trailing)
				i += 2 + end + 2
				continue
			}
		}
		// ~~strikethrough~~
		if strings.HasPrefix(s[i:], "~~") {
			if end := strings.Index(s[i+2:], "~~"); end >= 0 {
				leading, content, trailing := splitLeadingTrailingSpaces(s[i+2 : i+2+end])
				out.WriteString(leading + styleStrike.Render(content) + trailing)
				i += 2 + end + 2
				continue
			}
		}
		// *italic*
		if s[i] == '*' && (i == 0 || s[i-1] != '*') && (i+1 >= len(s) || s[i+1] != '*') {
			if end := strings.Index(s[i+1:], "*"); end >= 0 && !strings.HasPrefix(s[i+1+end:], "**") {
				leading, content, trailing := splitLeadingTrailingSpaces(s[i+1 : i+1+end])
				out.WriteString(leading + styleMuted.Render(content) + trailing)
				i += 1 + end + 1
				continue
			}
		}
		// `code`
		if s[i] == '`' {
			if end := strings.Index(s[i+1:], "`"); end >= 0 {
				leading, content, trailing := splitLeadingTrailingSpaces(s[i+1 : i+1+end])
				out.WriteString(leading + styleMDCode.Render(content) + trailing)
				i += 1 + end + 1
				continue
			}
		}
		out.WriteByte(s[i])
		i++
	}
	return out.String()
}

func splitLeadingTrailingSpaces(s string) (leading, content, trailing string) {
	start := 0
	for start < len(s) && (s[start] == ' ' || s[start] == '\t' || s[start] == '\n' || s[start] == '\r') {
		start++
	}
	if start == len(s) {
		return s, "", ""
	}
	end := len(s)
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\n' || s[end-1] == '\r') {
		end--
	}
	return s[:start], s[start:end], s[end:]
}

func stripInlineMarkdownForWidth(s string) string {
	s = strings.ReplaceAll(s, "**", "")
	s = strings.ReplaceAll(s, "__", "")
	s = strings.ReplaceAll(s, "*", "")
	s = strings.ReplaceAll(s, "_", "")
	s = strings.ReplaceAll(s, "`", "")
	s = strings.ReplaceAll(s, "~~", "")
	return s
}

func preprocessMarkdownTables(lines []string, width int) []string {
	out := make([]string, len(lines))
	copy(out, lines)
	states := codeStates(lines)
	for i := 0; i < len(out); i++ {
		t := strings.TrimSpace(out[i])
		if states[i] == notCode && strings.HasPrefix(t, "|") && strings.HasSuffix(t, "|") {
			end := i
			for end < len(out) {
				t2 := strings.TrimSpace(out[end])
				if states[end] != notCode || !(strings.HasPrefix(t2, "|") && strings.HasSuffix(t2, "|")) {
					break
				}
				end++
			}
			if end-i >= 2 {
				tableLines := formatMarkdownTable(out[i:end], width)
				for j := i; j < end; j++ {
					if j-i < len(tableLines) {
						out[j] = tableLines[j-i]
					}
				}
			}
			i = end - 1
		}
	}
	return out
}

func formatMarkdownTable(lines []string, width ...int) []string {
	if len(lines) < 2 {
		return lines
	}
	wMax := 0
	if len(width) > 0 {
		wMax = width[0]
	}
	var rows [][]string
	for _, l := range lines {
		cells := strings.Split(strings.Trim(strings.TrimSpace(l), "|"), "|")
		for i := range cells {
			cells[i] = strings.TrimSpace(cells[i])
		}
		rows = append(rows, cells)
	}

	colWidths := make([]int, 0)
	for i, row := range rows {
		if i == 1 && len(row) > 0 && strings.HasPrefix(row[0], "-") {
			continue // skip separator
		}
		for j, cell := range row {
			w := runewidth.StringWidth(stripInlineMarkdownForWidth(cell))
			if j >= len(colWidths) {
				colWidths = append(colWidths, w)
			} else if w > colWidths[j] {
				colWidths[j] = w
			}
		}
	}

	if wMax > 0 && len(colWidths) > 0 {
		numCols := len(colWidths)
		borders := 3*numCols + 1
		avail := wMax - borders
		if avail < numCols*3 {
			avail = numCols * 3
		}
		sum := 0
		for _, w := range colWidths {
			sum += w
		}
		for sum > avail {
			maxIdx := -1
			maxW := -1
			minW := max(3, avail/numCols)
			for j, w := range colWidths {
				if w > minW && w > maxW {
					maxW = w
					maxIdx = j
				}
			}
			if maxIdx == -1 {
				break
			}
			colWidths[maxIdx]--
			sum--
		}
	}

	var out []string
	for i, row := range rows {
		var sb strings.Builder
		if i == 1 && len(row) > 0 && strings.HasPrefix(row[0], "-") {
			sb.WriteString("├")
			for j, w := range colWidths {
				sb.WriteString(strings.Repeat("─", w+2))
				if j < len(colWidths)-1 {
					sb.WriteString("┼")
				}
			}
			sb.WriteString("┤")
		} else {
			sb.WriteString("│ ")
			for j, cell := range row {
				w := 0
				if j < len(colWidths) {
					w = colWidths[j]
				}
				cleanCell := stripInlineMarkdownForWidth(cell)
				actualW := runewidth.StringWidth(cleanCell)
				if actualW > w && w > 0 {
					cell = runewidth.Truncate(cleanCell, w, "…")
					actualW = runewidth.StringWidth(stripInlineMarkdownForWidth(cell))
				}
				sb.WriteString(cell)
				if w > actualW {
					sb.WriteString(strings.Repeat(" ", w-actualW))
				}
				if j < len(row)-1 {
					sb.WriteString(" │ ")
				}
			}
			sb.WriteString(" │")
		}
		out = append(out, sb.String())
	}
	return out
}
