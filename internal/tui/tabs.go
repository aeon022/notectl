package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/aeon022/missionctl-core/humanize"
	"github.com/charmbracelet/lipgloss"
)

// ── Account indicator + two-row notebook tabs ───────────────────────────────
//
// Row 1 lists the active account's top-level notebooks ("All" + first path
// segment of every folder in that account), horizontally scrollable when
// they overflow the terminal width. Row 2, shown only while a top-level
// notebook with subfolders is active, lists that notebook's direct
// children (e.g. "Projects" → "Git", "MISSIONCTL"), prefixed with the
// parent's own name so it's unambiguous which notebook it belongs to no
// matter where row 1 has scrolled to. tab/shift+tab walk a single
// flattened sequence: every top-level notebook immediately followed by
// its own children, in order (no separate "enter/leave sub-notebooks"
// mode — an earlier version used "]"/"[" for that and it just added a
// step for no real benefit). Selecting a top-level notebook aggregates it
// and all its descendants (see store.List's self+descendants folder
// filter); landing on one of its children narrows to that one subfolder.
//
// Accounts (Apple Notes only, e.g. "iCloud", "FH Burgenland") are a
// separate, orthogonal axis, deliberately NOT a third tab row — an
// earlier version tried that (a whole extra row of pills, plus a
// rowFocus concept to decide whether tab/shift+tab meant "walk notebooks"
// or "walk accounts") and it was both visually noisy and confusing: two
// keys doing different things depending on invisible state nobody could
// see. Instead "["/"]" always and only cycle the active account —
// immediate, visible effect (the header's account indicator changes, row
// 1 reloads scoped to it), no focus mode to get stuck in. See
// ListApple's doc comment for why accounts need to be scoped at all:
// different accounts can have identically-named top-level notebooks
// (e.g. two different "Notizen"), which a flat, account-unaware notebook
// tree would otherwise merge into one tab.

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

// ── Accounts (header indicator, not a tab row) ──────────────────────────

// hasMultipleAccounts reports whether there's more than one account to
// disambiguate — a single account (or no account concept at all, e.g. an
// obsidian vault) has nothing for the indicator/["/"]" to do.
func (m Model) hasMultipleAccounts() bool {
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
// "All accounts" (also "" when there's no account concept at all, which
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

// accountIndicator renders the compact "‹accountName› (i/n)" (or "All
// accounts (n)") shown in the header — "" when there's nothing to
// disambiguate, so renderAppHeader can just skip it.
func (m Model) accountIndicator() string {
	if !m.hasMultipleAccounts() {
		return ""
	}
	c := m.currentAccountCursor()
	if c == 0 {
		return fmt.Sprintf("All accounts (%d)", len(m.accounts))
	}
	return fmt.Sprintf("%s (%d/%d)", m.accounts[c-1], c, len(m.accounts))
}

// notebookAccountIndicator reports which account(s) the notes currently on
// screen actually live in, distinct from accountIndicator (which only shows
// the active FILTER tab, e.g. "All accounts (7)"). Under "All accounts", a
// notebook name like "Notizen" can be a merge of several accounts' own
// same-named folders (see buildFolderTree's doc comment above) — without
// this, a note's real Apple Notes account was invisible everywhere in the
// UI once you'd navigated into a notebook, so a note could look like it
// belonged to whichever account you last had active, when it actually sat
// in a different one entirely (confirmed live: a note surfaced under the
// "Notizen" tab while last on the "Die Brücke || Gerwin" account turned out
// to actually live in a third, unrelated Exchange account's own "Notizen").
// mixed is true when the on-screen notes span more than one account, so the
// caller can render it as a warning instead of a plain label. Returns ""
// when there's no notebook selected, no notes loaded yet, or no account
// concept at all (nothing to disambiguate).
func (m Model) notebookAccountIndicator() (label string, mixed bool) {
	if !m.hasMultipleAccounts() || m.activeFolder() == "" || len(m.notes) == 0 {
		return "", false
	}
	seen := map[string]bool{}
	var distinct []string
	for _, n := range m.notes {
		if n.Account == "" || seen[n.Account] {
			continue
		}
		seen[n.Account] = true
		distinct = append(distinct, n.Account)
	}
	switch len(distinct) {
	case 0:
		return "", false
	case 1:
		return distinct[0], false
	default:
		sort.Strings(distinct)
		return fmt.Sprintf("⚠ %d accounts mixed here", len(distinct)), true
	}
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
// top-level bar (index into m.topFolders+1, "All" is 0), row 1 for the
// sub-notebook bar (index into the active top-level notebook's children).
// row is -1 if the click missed both.
func (m Model) tabHitTest(x, y int) (row, idx int) {
	if y == 1 {
		labels := m.topLabels()
		start, end := tabWindow(labels, m.currentPos().top, m.tabScroll, m.width-1)
		col := 1
		if start > 0 {
			col += lipgloss.Width("‹") + 2
		}
		for i := start; i < end; i++ {
			ww := tabWidth(labels[i], i == m.currentPos().top)
			if x >= col && x < col+ww {
				return 0, i
			}
			col += ww + 2
		}
		return -1, -1
	}
	if y == 2 {
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
				return 1, i
			}
			col += ww + 2
		}
		return -1, -1
	}
	return -1, -1
}
