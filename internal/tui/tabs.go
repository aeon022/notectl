package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/aeon022/missionctl-core/humanize"
	"github.com/charmbracelet/lipgloss"
)

// ── Account row + two-row notebook tabs ─────────────────────────────────────
//
// Row 0 (optional, only when there's more than one account — see
// Model.accounts) lists Apple Notes accounts ("All accounts" + each
// account name), e.g. "iCloud", "FH Burgenland". Row 1 lists the active
// account's top-level notebooks ("All" + first path segment of every
// folder in that account), horizontally scrollable when they overflow the
// terminal width. Row 2, shown only while a top-level notebook with
// subfolders is active, lists that notebook's direct children (e.g.
// "Projects" → "Git", "MISSIONCTL"), prefixed with the parent's own name
// so it's unambiguous which notebook it belongs to no matter where row 1
// has scrolled to.
//
// tab/shift+tab walk a single flattened sequence within whichever row
// rowFocus currently points at: on row 1, every top-level notebook
// immediately followed by its own children, in order (no separate
// "enter/leave sub-notebooks" mode — an earlier version used "]"/"[" for
// that and it just added a step for no real benefit); on row 0, every
// account. "]"/"[" now switch rowFocus itself between the two rows — a
// different job than sub-notebook depth, which tab/shift+tab already
// handle within row 1. Selecting a top-level notebook aggregates it and
// all its descendants (see store.List's self+descendants folder filter);
// landing on one of its children narrows to that one subfolder. Selecting
// an account reloads row 1 scoped to just that account's own folders (see
// ListApple's doc comment for why: different accounts can have
// identically-named top-level folders, e.g. two different "Notizen").

// rowFocus is which tab row tab/shift+tab currently walk.
type rowFocus int

const (
	focusNotebook rowFocus = iota // default — row 1/2, as before accounts existed
	focusAccount                  // row 0
)

// buildFolderTree splits the flat, full-path folder list (as stored on each
// note, e.g. "Projects/Git") into top-level notebooks and, per top-level
// notebook, its direct children — full paths, sorted. Folders nested more
// than one level deep still work as filters (store.List matches them via
// prefix) but only their first-level ancestor shows up as its own row-2 tab;
// deeper levels aren't rendered as a third row.
func buildFolderTree(folders []string) (tops []string, children map[string][]string) {
	children = map[string][]string{}
	seen := map[string]bool{}
	for _, f := range folders {
		top := f
		if i := strings.IndexByte(f, '/'); i >= 0 {
			top = f[:i]
			children[top] = append(children[top], f)
		}
		if !seen[top] {
			seen[top] = true
			tops = append(tops, top)
		}
	}
	sort.Strings(tops)
	for k := range children {
		sort.Strings(children[k])
	}
	return tops, children
}

// tabPos is one stop in the flattened tab/shift+tab sequence: top is 0 for
// "All" or a 1-based index into topFolders; sub is -1 for the top-level
// notebook itself (aggregate) or an index into its children.
type tabPos struct{ top, sub int }

// tabPositions is the full flattened sequence tab/shift+tab step through.
func (m Model) tabPositions() []tabPos {
	positions := make([]tabPos, 0, len(m.topFolders)+1)
	positions = append(positions, tabPos{0, -1})
	for i, t := range m.topFolders {
		positions = append(positions, tabPos{i + 1, -1})
		for j := range m.subFolders[t] {
			positions = append(positions, tabPos{i + 1, j})
		}
	}
	return positions
}

// currentPos resolves m.tabCursor to a position, clamping defensively to
// "All" if folders changed (e.g. a sync removed one) and the old cursor no
// longer lands anywhere sensible.
func (m Model) currentPos() tabPos {
	positions := m.tabPositions()
	if m.tabCursor < 0 || m.tabCursor >= len(positions) {
		return tabPos{0, -1}
	}
	return positions[m.tabCursor]
}

// cursorFor finds the flattened index for an explicit (top, sub) position —
// used by mouse clicks, which know exactly which tab they landed on.
func (m Model) cursorFor(top, sub int) int {
	for i, p := range m.tabPositions() {
		if p.top == top && p.sub == sub {
			return i
		}
	}
	return 0
}

// resolveTabCursor finds where a full folder path (as persisted in uistate)
// sits in the current tree, for restoring it on startup. ok is false if
// path isn't a known top-level or child notebook (e.g. deleted since last
// run).
func (m Model) resolveTabCursor(path string) (int, bool) {
	for i, p := range m.tabPositions() {
		if p.top == 0 {
			continue
		}
		top := m.topFolders[p.top-1]
		full := top
		if p.sub >= 0 {
			full = m.subFolders[top][p.sub]
		}
		if full == path {
			return i, true
		}
	}
	return 0, false
}

// activeChildren returns the currently active top-level notebook's children
// (nil if "All" is active or it has none).
func (m Model) activeChildren() []string {
	pos := m.currentPos()
	if pos.top == 0 || pos.top > len(m.topFolders) {
		return nil
	}
	return m.subFolders[m.topFolders[pos.top-1]]
}

// ── Row 0: accounts ──────────────────────────────────────────────────────

// showAccountRow reports whether row 0 is worth rendering at all — a
// single account (or no account concept, e.g. an obsidian vault) has
// nothing to disambiguate.
func (m Model) showAccountRow() bool {
	return len(m.accounts) > 1
}

// currentAccountCursor clamps m.accountCursor into range, defensively —
// same reasoning as currentPos: accounts can change (e.g. after a sync
// that added one) out from under a cursor set before the change.
func (m Model) currentAccountCursor() int {
	if m.accountCursor < 0 || m.accountCursor > len(m.accounts) {
		return 0
	}
	return m.accountCursor
}

// activeAccount returns the currently selected account name, or "" for
// "All accounts" (also "" when there's no account row at all, which
// naturally falls out of accounts being empty).
func (m Model) activeAccount() string {
	c := m.currentAccountCursor()
	if c == 0 || c > len(m.accounts) {
		return ""
	}
	return m.accounts[c-1]
}

// resolveAccountCursor finds where a persisted account name sits in the
// current account list, for restoring it on startup. ok is false if it's
// not a known account (e.g. that Notes.app account was removed since).
func (m Model) resolveAccountCursor(account string) (int, bool) {
	for i, a := range m.accounts {
		if a == account {
			return i + 1, true
		}
	}
	return 0, false
}

func (m Model) accountLabels() []string {
	labels := make([]string, 0, len(m.accounts)+1)
	labels = append(labels, tabLabel("All accounts", m.accountCounts[""]))
	for _, a := range m.accounts {
		labels = append(labels, tabLabel(a, m.accountCounts[a]))
	}
	return labels
}

// ensureAccountVisible reclamps m.accountScroll so the active account tab
// is within the rendered window — same mechanics as ensureTabVisible.
func (m *Model) ensureAccountVisible() {
	w := m.width - 1
	if w < 1 {
		w = 1
	}
	scroll, _ := tabWindow(m.accountLabels(), m.currentAccountCursor(), m.accountScroll, w)
	m.accountScroll = scroll
}

// renderTabRow0 renders the account tab bar. Callers should check
// showAccountRow first (see listStartY/preambleRows) — rendering it
// unconditionally would cost a line even for a single-account setup with
// nothing to disambiguate.
func (m Model) renderTabRow0(w int) string {
	labels := m.accountLabels()
	active := m.currentAccountCursor()
	start, end := tabWindow(labels, active, m.accountScroll, w)
	var parts []string
	if start > 0 {
		parts = append(parts, styleMuted.Render("‹"))
	}
	for i := start; i < end; i++ {
		style := styleTabInact
		if i == active {
			style = styleTabActive
		}
		parts = append(parts, style.Render(labels[i]))
	}
	if end < len(labels) {
		parts = append(parts, styleMuted.Render("›"))
	}
	return strings.Join(parts, "  ")
}

func tabLabel(display string, count int) string {
	if count > 0 {
		return fmt.Sprintf("%s %d", display, count)
	}
	return display
}

func (m Model) topLabels() []string {
	labels := make([]string, 0, len(m.topFolders)+1)
	labels = append(labels, tabLabel("All", m.folderCounts[""]))
	for _, t := range m.topFolders {
		labels = append(labels, tabLabel(t, m.folderCounts[t]))
	}
	return labels
}

func (m Model) subLabels(top string) []string {
	kids := m.subFolders[top]
	labels := make([]string, len(kids))
	for i, k := range kids {
		leaf := strings.TrimPrefix(k, top+"/")
		labels[i] = tabLabel(leaf, m.folderCounts[k])
	}
	return labels
}

func tabWidth(label string, active bool) int {
	if active {
		return lipgloss.Width(styleTabActive.Render(label))
	}
	return lipgloss.Width(styleTabInact.Render(label))
}

// tabWindow grows a visible window of tabs starting at scroll until it no
// longer fits w columns, then — if that window doesn't include activeIdx —
// advances scroll and retries, so the active tab is always on screen. It
// always includes at least one tab even if that single tab alone overflows
// w, so a very narrow terminal still shows something clickable.
func tabWindow(labels []string, activeIdx, scroll, w int) (start, end int) {
	if scroll < 0 {
		scroll = 0
	}
	if scroll > activeIdx {
		scroll = activeIdx
	}
	for {
		total := 0
		end = scroll
		for end < len(labels) {
			ww := tabWidth(labels[end], end == activeIdx) + 2
			if total+ww > w && end > scroll {
				break
			}
			total += ww
			end++
		}
		if activeIdx < end {
			break
		}
		scroll++
	}
	return scroll, end
}

// ensureTabVisible reclamps m.tabScroll so the active top-level tab is
// within the rendered window at the current terminal width — called
// whenever the tab cursor or the terminal width changes.
func (m *Model) ensureTabVisible() {
	labels := m.topLabels()
	w := m.width - 1
	if w < 1 {
		w = 1
	}
	scroll, _ := tabWindow(labels, m.currentPos().top, m.tabScroll, w)
	m.tabScroll = scroll
}

func (m Model) renderTabRow1(w int) string {
	labels := m.topLabels()
	pos := m.currentPos()
	start, end := tabWindow(labels, pos.top, m.tabScroll, w)
	var parts []string
	if start > 0 {
		parts = append(parts, styleMuted.Render("‹"))
	}
	for i := start; i < end; i++ {
		style := styleTabInact
		if i == pos.top && pos.sub < 0 {
			style = styleTabActive
		} else if i == pos.top {
			// A child of this notebook is what's actually selected (see
			// row 2) — keep the parent visibly current but recede it a
			// touch so the child reads as the real focus.
			style = styleTabActiveDim
		}
		parts = append(parts, style.Render(labels[i]))
	}
	if end < len(labels) {
		parts = append(parts, styleMuted.Render("›"))
	}
	bar := strings.Join(parts, "  ")
	if m.syncing {
		bar += "  " + m.sp.View() + styleSyncing.Render(" syncing…")
	} else if !m.lastSynced.IsZero() {
		bar += "  " + styleMuted.Render("synced "+humanize.TimeAgo(m.lastSynced))
	}
	return bar
}

// subTabPrefix renders the "<Parent> › " lead-in that opens row 2 — naming
// the parent explicitly (rather than a bare arrow) so it's unambiguous
// which notebook row 2 belongs to even when row 1 has scrolled and the
// parent tab itself isn't visible anymore.
func subTabPrefix(top string) string {
	return styleTabParentRef.Render(top) + styleMuted.Render(" › ")
}

// renderTabRow2 renders the active top-level notebook's children, if any —
// callers should skip this row entirely (and the extra line it costs, see
// listStartY) when it returns "". Deliberately plain text rather than the
// filled pill style row 1 uses: row 1 is few, fixed, always-there
// destinations, while row 2's contents change with every parent and read
// better as a lightweight breadcrumb than as another row of buttons. No
// independent scrolling: sub-notebook counts are expected to stay small, so
// overflow just truncates with an ellipsis rather than growing a second
// scroll mechanism.
func (m Model) renderTabRow2(w int) string {
	kids := m.activeChildren()
	if len(kids) == 0 {
		return ""
	}
	pos := m.currentPos()
	top := m.topFolders[pos.top-1]
	prefix := subTabPrefix(top)
	labels := m.subLabels(top)
	var parts []string
	total := lipgloss.Width(prefix)
	for i, l := range labels {
		style := styleSubInact
		if i == pos.sub {
			style = styleSubActive
		}
		rendered := style.Render(l)
		ww := lipgloss.Width(rendered) + 2
		if total+ww > w && len(parts) > 0 {
			parts = append(parts, styleMuted.Render("…"))
			break
		}
		total += ww
		parts = append(parts, rendered)
	}
	return "  " + prefix + strings.Join(parts, "  ")
}

// tabHitTest returns which tab a mouse click landed on: row 0 for the
// account bar (index into m.accounts+1, "All accounts" is 0), row 1 for
// the notebook top-level bar (index into m.topFolders+1, "All" is 0), row
// 2 for the sub-notebook bar (index into the active top-level notebook's
// children). row is -1 if the click missed all three. The account bar's
// own y only exists when showAccountRow is true, which shifts row 1/2
// down by one line — the same shift preambleRows/listStartY apply to the
// note list below, so this can't drift out of sync with what's actually
// on screen.
func (m Model) tabHitTest(x, y int) (row, idx int) {
	nextY := 1 // row 0 (or row 1, if no account row) starts right below the header
	if m.showAccountRow() {
		if y == nextY {
			labels := m.accountLabels()
			active := m.currentAccountCursor()
			start, end := tabWindow(labels, active, m.accountScroll, m.width-1)
			col := 1
			if start > 0 {
				col += lipgloss.Width("‹") + 2
			}
			for i := start; i < end; i++ {
				ww := tabWidth(labels[i], i == active)
				if x >= col && x < col+ww {
					return 0, i
				}
				col += ww + 2
			}
			return -1, -1
		}
		nextY++
	}
	if y == nextY {
		labels := m.topLabels()
		start, end := tabWindow(labels, m.currentPos().top, m.tabScroll, m.width-1)
		col := 1
		if start > 0 {
			col += lipgloss.Width("‹") + 2
		}
		for i := start; i < end; i++ {
			ww := tabWidth(labels[i], i == m.currentPos().top)
			if x >= col && x < col+ww {
				return 1, i
			}
			col += ww + 2
		}
		return -1, -1
	}
	nextY++
	if y == nextY {
		kids := m.activeChildren()
		if len(kids) == 0 {
			return -1, -1
		}
		pos := m.currentPos()
		top := m.topFolders[pos.top-1]
		labels := m.subLabels(top)
		col := 1 + lipgloss.Width(subTabPrefix(top))
		for i, l := range labels {
			ww := lipgloss.Width(styleSubInact.Render(l))
			if i == pos.sub {
				ww = lipgloss.Width(styleSubActive.Render(l))
			}
			if x >= col && x < col+ww {
				return 2, i
			}
			col += ww + 2
		}
		return -1, -1
	}
	return -1, -1
}
