package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/aeon022/missionctl-core/humanize"
	"github.com/aeon022/notectl/internal/store"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

// ── Account line + two-row notebook tabs ────────────────────────────────────
//
// Row 1 lists the active account's top-level notebooks ("All" + first path
// segment of every folder in that account), horizontally scrollable when
// they overflow the terminal width. A notebook with children shows a
// disclosure marker ("▸" collapsed, "▾" expanded — see topFolderLabel) so
// it's visible on row 1 alone that it has something underneath, without
// having to select it first. Row 2, shown only for a top-level notebook
// that's been explicitly expanded (see expandedTops on the Model — "right"/
// "l" expands or steps into the first child, "left"/"h" collapses or steps
// up to the parent), lists that notebook's direct children (e.g.
// "Projects" → "Git", "MISSIONCTL"), prefixed with the parent's own name so
// it's unambiguous which notebook it belongs to even when row 1 has
// scrolled and the parent itself isn't visible anymore. tab/shift+tab walk
// a single flattened sequence: every top-level notebook, and — only while
// expanded — its own children right after it (children of a collapsed
// notebook are simply absent from the sequence, not skipped over). An
// earlier version auto-revealed row 2 the instant a notebook with children
// became the active top-level selection, with tab/shift+tab always walking
// straight through every notebook's children whether you wanted to see
// them or not — replaced with this explicit expand step because that
// auto-reveal made simply glancing across the top-level row impossible.
// Selecting a top-level notebook aggregates it and all its descendants
// (see store.List's self+descendants folder filter) regardless of whether
// it's expanded; landing on one of its children narrows to that one
// subfolder.
//
// Accounts (Apple Notes only, e.g. "iCloud", "FH Burgenland") are a
// separate, orthogonal axis, deliberately NOT a third tab row — an
// earlier version tried that (a whole extra row of pills, plus a
// rowFocus concept to decide whether tab/shift+tab meant "walk notebooks"
// or "walk accounts") and it was both visually noisy and confusing: two
// keys doing different things depending on invisible state nobody could
// see. Instead "["/"]" always and only cycle the active account —
// immediate, visible effect (renderAccountLine changes, row 1 reloads
// scoped to it), no focus mode to get stuck in. See ListApple's doc
// comment for why accounts need to be scoped at all: different accounts
// can have identically-named top-level notebooks (e.g. two different
// "Notizen"). buildAccountAwareFolderTree is what keeps those from
// merging into one ambiguous tab under "All accounts" — see its own doc
// comment — splitting them into one tab per account instead, with
// renderAccountLine (not the tab label itself) naming which account a
// given split tab is bound to.

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

// filterEmptyFolders drops any folder path with a zero count in counts,
// when hideEmpty is set — used for the single-account tab tree (a specific
// account is already unambiguous, so it never needs the collision-splitting
// buildAccountAwareFolderTree does, but still respects the same
// hideEmptyNotebooks preference).
func filterEmptyFolders(folders []string, counts map[string]int, hideEmpty bool) []string {
	if !hideEmpty {
		return folders
	}
	out := make([]string, 0, len(folders))
	for _, f := range folders {
		if counts[f] > 0 {
			out = append(out, f)
		}
	}
	return out
}

// buildAccountAwareFolderTree is buildFolderTree's "All accounts" sibling:
// same top-level/children split from a flat folder list, but additionally
// splits a top-level notebook into one tab per account whenever its name
// collides across more than one Apple Notes account — e.g. two Exchange
// accounts each with their own "Notizen" — instead of merging them into one
// tab that silently mixed both accounts' notes together (the exact
// confusion notebookAccountIndicator was built to at least flag; this
// removes the ambiguity at the source instead). A non-colliding folder
// keeps the old, account-agnostic single tab (topAccounts entry "") —
// scoped by whatever the global "["/"]" account filter is, unchanged.
//
// Within a collision, accounts are ordered by their own note count in that
// notebook descending — the account that actually has content leads, an
// empty sibling account's copy trails — then by account name as a stable
// tie-break.
//
// folders/counts are the ordinary unscoped ("All accounts") data
// buildFolderTree itself would use; counts is written into (not just read)
// as this runs — a colliding tab's own per-account count gets added under
// its composite topFolderKey (folder+"\x00"+account) so topLabels can look
// it up the same way it looks up an ordinary tab's count, since the plain
// folder-name key can't hold two different numbers for the same name.
// perAccount is one FolderInfo slice per Apple Notes account
// (store.ListFolderInfoByAccount's shape), used only to detect which
// top-level names actually collide and each account's own count for them.
// hideEmpty drops any resulting top-level tab (colliding or not) whose
// count is 0.
//
// Sub-notebooks (row 2) are deliberately NOT split per account even under a
// colliding top — a real edge case (two accounts each nesting their own
// same-named subfolder under their own same-named top) that isn't the
// problem this exists to solve; children stay keyed by the plain top name,
// shared across whichever accounts that top spans.
func buildAccountAwareFolderTree(folders []string, counts map[string]int, perAccount map[string][]store.FolderInfo, hideEmpty bool) (tops []string, topAccounts []string, children map[string][]string) {
	// topAccountCounts[topName][account] = that account's own rolled-up
	// count for topName — only ever has more than one entry for a name
	// that's a real collision.
	topAccountCounts := map[string]map[string]int{}
	for account, infos := range perAccount {
		for _, info := range infos {
			top := info.Folder
			if i := strings.IndexByte(top, '/'); i >= 0 {
				top = top[:i]
			}
			if info.Folder != top {
				continue // only the top-level entry itself, not a nested child
			}
			if topAccountCounts[top] == nil {
				topAccountCounts[top] = map[string]int{}
			}
			topAccountCounts[top][account] = info.Count
		}
	}

	plainTops, plainChildren := buildFolderTree(folders)
	children = plainChildren

	for _, top := range plainTops {
		byAccount := topAccountCounts[top]
		if len(byAccount) < 2 {
			if hideEmpty && counts[top] == 0 {
				continue
			}
			tops = append(tops, top)
			topAccounts = append(topAccounts, "")
			continue
		}
		type acctCount struct {
			account string
			count   int
		}
		entries := make([]acctCount, 0, len(byAccount))
		for account, c := range byAccount {
			entries = append(entries, acctCount{account, c})
		}
		sort.Slice(entries, func(i, j int) bool {
			if entries[i].count != entries[j].count {
				return entries[i].count > entries[j].count
			}
			return entries[i].account < entries[j].account
		})
		for _, e := range entries {
			if hideEmpty && e.count == 0 {
				continue
			}
			if counts != nil {
				counts[top+"\x00"+e.account] = e.count
			}
			tops = append(tops, top)
			topAccounts = append(topAccounts, e.account)
		}
	}
	return tops, topAccounts, children
}

// tabPos is one stop in the flattened tab/shift+tab sequence: top is 0 for
// "All" or a 1-based index into topFolders; sub is -1 for the top-level
// notebook itself (aggregate) or an index into its children.
type tabPos struct{ top, sub int }

// tabPositions is the full flattened sequence tab/shift+tab step through —
// every top-level notebook, plus (only for one that's been explicitly
// expanded, see expandedTops) its own children right after it. A collapsed
// notebook's children are simply absent from the sequence: tab/shift+tab
// skip straight past them until "right"/"l" expands it.
func (m Model) tabPositions() []tabPos {
	positions := make([]tabPos, 0, len(m.topFolders)+1)
	positions = append(positions, tabPos{0, -1})
	for i, t := range m.topFolders {
		positions = append(positions, tabPos{i + 1, -1})
		if !m.isExpanded(i) {
			continue
		}
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

// activeFolderAccount returns the account the current top-level tab is
// bound to (see topFolderAccounts' doc comment on the Model field) — "" for
// an ordinary, account-agnostic tab, "All", or an out-of-range position.
func (m Model) activeFolderAccount() string {
	pos := m.currentPos()
	if pos.top == 0 || pos.top > len(m.topFolderAccounts) {
		return ""
	}
	return m.topFolderAccounts[pos.top-1]
}

// effectiveAccount is what loadNotesCmd should actually filter by: the
// current tab's own bound account if it has one (a collision-split tab —
// see topFolderAccounts), overriding the global "["/"]" account filter for
// exactly that tab; otherwise activeAccount() as before.
func (m Model) effectiveAccount() string {
	if a := m.activeFolderAccount(); a != "" {
		return a
	}
	return m.activeAccount()
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

// renderAccountLine is the always-visible, full-width line just below the
// app header showing which account is actually in play — replacing the old
// accountIndicator-in-the-header approach, which had to cram a possibly
// long account name (a 36-char Exchange UPN, seen live) in next to
// "notectl" and the date on one shared line; that budget pressure was what
// previously caused the header to silently overflow and get overwritten by
// the next redraw. Getting its own line removes the budget problem instead
// of working around it.
//
// Shows the current top-level tab's own bound account (see
// topFolderAccounts) when it has one — a collision-split tab (e.g. one of
// two "Notizen" tabs) is unambiguous on its own, so this updates live as
// you move between them, telling you exactly which account you're looking
// at without the tab label itself needing to say so. Otherwise falls back
// to accountIndicator's "All accounts (n)" / "<account> (i/n)" — the global
// "["/"]" filter state. "" when there's nothing to disambiguate (a single
// account, or no account concept at all), so callers can skip the row.
func (m Model) renderAccountLine(w int) string {
	if !m.hasMultipleAccounts() {
		return ""
	}
	text := m.accountIndicator()
	if a := m.activeFolderAccount(); a != "" {
		text = "Account: " + a
	}
	return styleTabParentRef.Render(runewidth.Truncate(text, w, "…"))
}

func tabLabel(display string, count int) string {
	if count > 0 {
		return fmt.Sprintf("%s %d", display, count)
	}
	return display
}

// topFolderKey returns the key used to look up tab i's own note count in
// m.folderCounts — the plain folder path for an ordinary tab, or a
// folder+account composite for one half of a same-named collision (see
// buildAccountAwareFolderTree) so two colliding tabs sharing the same
// folder name don't also collide on their count lookup. Never rendered
// directly; topFolderLabel below is what's actually shown.
func (m Model) topFolderKey(i int) string {
	top := m.topFolders[i]
	if i < len(m.topFolderAccounts) && m.topFolderAccounts[i] != "" {
		return top + "\x00" + m.topFolderAccounts[i]
	}
	return top
}

// hasChildren reports whether top-level tab i has any row-2 sub-notebooks —
// independent of whether it's currently expanded, so topFolderLabel can
// show the disclosure marker on every parent, not just the active one.
func (m Model) hasChildren(i int) bool {
	if i < 0 || i >= len(m.topFolders) {
		return false
	}
	return len(m.subFolders[m.topFolders[i]]) > 0
}

// isExpanded reports whether top-level tab i currently shows its children
// in row 2 — see expandedTops' doc comment on the Model field.
func (m Model) isExpanded(i int) bool {
	if i < 0 || i >= len(m.topFolders) || m.expandedTops == nil {
		return false
	}
	return m.expandedTops[m.topFolderKey(i)]
}

// setExpanded sets whether top-level tab i shows its children in row 2.
func (m *Model) setExpanded(i int, expanded bool) {
	if i < 0 || i >= len(m.topFolders) {
		return
	}
	if m.expandedTops == nil {
		m.expandedTops = map[string]bool{}
	}
	key := m.topFolderKey(i)
	if expanded {
		m.expandedTops[key] = true
	} else {
		delete(m.expandedTops, key)
	}
}

// topFolderNameWidth caps a single top-level tab's own folder-name text —
// separately from the count/marker tabLabel adds — so one long notebook
// name can't dominate row 1's width budget on its own. Uncapped, a name
// like "Change-Management" (27 rendered columns) could force tabWindow to
// evict two or more tabs at once for a single tab/shift+tab step — you'd
// press it once and watch several previously-visible tabs vanish
// together, not just the one that had to make room (confirmed live: a
// single "tab" press past "Change-Management" dropped both it and "Baby"
// in the same step). Capping every name to the same modest width bounds
// how much any one tab can cost, so a single step forward only ever has to
// evict close to one tab's worth of space, not several.
const topFolderNameWidth = 14

// topFolderLabel is what tab i actually displays: the folder name (capped
// to topFolderNameWidth), prefixed with a disclosure marker when it has
// children — "▸" collapsed, "▾" expanded — so a parent notebook's own
// existence is visible on row 1 without having to select it first
// (previously the only way to discover a notebook had children at all was
// to tab onto it and watch row 2 pop up). The tab's bound account, if any
// (see topFolderAccounts), is deliberately NOT in this label — that lives
// on renderAccountLine instead, so a same-named collision's two tabs still
// read as plain, short notebook names rather than one of them carrying a
// 36-char Exchange address.
func (m Model) topFolderLabel(i int) string {
	top := runewidth.Truncate(m.topFolders[i], topFolderNameWidth, "…")
	if !m.hasChildren(i) {
		return top
	}
	if m.isExpanded(i) {
		return "▾ " + top
	}
	return "▸ " + top
}

func (m Model) topLabels() []string {
	labels := make([]string, 0, len(m.topFolders)+1)
	labels = append(labels, tabLabel("All", m.folderCounts[""]))
	for i := range m.topFolders {
		count := m.folderCounts[m.topFolderKey(i)]
		labels = append(labels, tabLabel(m.topFolderLabel(i), count))
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
	pos := m.currentPos()
	if pos.top == 0 || pos.top > len(m.topFolders) || !m.isExpanded(pos.top-1) {
		return ""
	}
	kids := m.activeChildren()
	if len(kids) == 0 {
		return ""
	}
	top := m.topFolders[pos.top-1]
	prefix := subTabPrefix(m.topFolderLabel(pos.top - 1))
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
		pos := m.currentPos()
		if pos.top == 0 || pos.top > len(m.topFolders) || !m.isExpanded(pos.top-1) {
			return -1, -1
		}
		kids := m.activeChildren()
		if len(kids) == 0 {
			return -1, -1
		}
		top := m.topFolders[pos.top-1]
		labels := m.subLabels(top)
		col := 1 + lipgloss.Width(subTabPrefix(m.topFolderLabel(pos.top-1)))
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
