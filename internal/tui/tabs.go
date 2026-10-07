package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/aeon022/missionctl-core/humanize"
	"github.com/aeon022/missionctl-core/ui"
	"github.com/aeon022/notectl/internal/store"
	"github.com/charmbracelet/x/ansi"
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

// headerContext is the account text shown in the middle of the app header
// ("Account: <bound account>" for a tab bound to one, else accountIndicator's
// "All accounts (n)" / "<account> (i/n)"), "" when there's nothing to
// disambiguate. ui.Header drops it first, then truncates the app name, when
// the line is too narrow — so a 36-char Exchange UPN can no longer overflow
// the header the way it did before the account got its own line (and its own
// preamble row, which is gone again now that the header carries it).
func (m Model) headerContext() string {
	if !m.hasMultipleAccounts() {
		return ""
	}
	if a := m.activeFolderAccount(); a != "" {
		return "Account: " + a
	}
	return m.accountIndicator()
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

// topFolderNameWidth caps a single top-level tab's own folder-name text, so
// one absurdly long notebook name can't take the whole row. ui.Tabs windows
// around the active tab, so ordinary names ("Change-Management") show whole.
const topFolderNameWidth = 24

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

// topLabels are the row-1 tab names (disclosure marker included, no count)
// and topCounts their note counts — ui.Tabs puts the count after the name.
func (m Model) topLabels() []string {
	labels := make([]string, 0, len(m.topFolders)+1)
	labels = append(labels, "All")
	for i := range m.topFolders {
		labels = append(labels, m.topFolderLabel(i))
	}
	return labels
}

func (m Model) topCounts() []int {
	counts := make([]int, 0, len(m.topFolders)+1)
	counts = append(counts, m.folderCounts[""])
	for i := range m.topFolders {
		counts = append(counts, m.folderCounts[m.topFolderKey(i)])
	}
	return counts
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

// ── Header chrome tiers ───────────────────────────────────────────────────────
//
// Two layouts, chosen by height (m.height is the terminal height minus the
// one reserve row set in the WindowSizeMsg handler, hence 29 for "30 rows"):
//
//	spacious (>= 30 rows)         compact (< 30 rows)
//	  header                        header
//	  ──────────                    Notebooks tabs
//	  (blank)                       [Folders chips]
//	  Notebooks  tabs               ──────────
//	  [Folders   chips]
//	  (blank)
//
// chrome() is the single source of truth for which screen row holds what, so
// the renderer, listStartY and the click tests can't drift apart.

const spaciousMinHeight = 29

func (m Model) spacious() bool { return m.height >= spaciousMinHeight }

// rowLabelW is the width of the dim "Notebooks"/"Folders" row label (spacious
// tier only) — both labels are padded to it so the chips line up.
const rowLabelW = 11

func (m Model) rowLabelW() int {
	if m.spacious() {
		return rowLabelW
	}
	return 0
}

func rowLabel(name string) string { return styleMuted.Render(fmt.Sprintf("%-*s", rowLabelW, name)) }

// hasFolderRow reports whether the active notebook is expanded and has
// sub-folders, i.e. the Folders row exists.
func (m Model) hasFolderRow() bool {
	pos := m.currentPos()
	return pos.top > 0 && pos.top <= len(m.topFolders) && m.isExpanded(pos.top-1) && len(m.activeChildren()) > 0
}

type chrome struct {
	tabsY, foldersY int // screen rows; foldersY is -1 without a Folders row
	rows            int // lines above the list pane
}

func (m Model) chrome() chrome {
	folders := m.hasFolderRow()
	if m.spacious() {
		c := chrome{tabsY: 3, foldersY: -1, rows: 5} // header, divider, blank, tabs, blank
		if folders {
			c.foldersY, c.rows = 4, 6
		}
		return c
	}
	c := chrome{tabsY: 1, foldersY: -1, rows: 3} // header, tabs, divider
	if folders {
		c.foldersY, c.rows = 2, 4
	}
	return c
}

// syncStatus is the sync text shown in the footer ("syncing…" / "synced 3m
// ago"), amber once the last sync is over a day old. It used to share row 1
// with the tabs, which squeezed both.
func (m Model) syncStatus() string {
	if m.syncing {
		return m.sp.View() + styleSyncing.Render(" syncing…")
	}
	if m.lastSynced.IsZero() {
		return ""
	}
	txt := "synced " + humanize.TimeAgo(m.lastSynced)
	if time.Since(m.lastSynced) > 24*time.Hour {
		return styleSyncing.Render(txt)
	}
	return styleMuted.Render(txt)
}

// tabHit is the column span [x, x+w) of one visible tab or folder chip,
// relative to the start of its row text (the caller's 1-column margin is not
// included).
type tabHit struct{ idx, x, w int }

// tabBar draws the Notebooks row with ui.Tabs (active pill, others dimmed,
// count after the name; tabs far from the active one fold into "…") within w
// columns, and works out where each visible tab sits by finding its
// " label count " cell in the plain text. ui.Tabs only drops tabs from the
// ends, so the visible ones are contiguous around the active tab. Shared by
// renderTabRow1 and tabHitTest so drawing and clicking can't drift apart.
func (m Model) tabBar(w int) (string, []tabHit) {
	lw := m.rowLabelW()
	label := ""
	if lw > 0 {
		label = rowLabel("Notebooks")
	}
	labels, counts, active := m.topLabels(), m.topCounts(), m.currentPos().top
	bar := ui.Tabs(max(w-lw, 1), labels, active, counts)
	plain := ansi.Strip(bar)
	seg := func(i int) string {
		if counts[i] > 0 {
			return fmt.Sprintf(" %s %d ", labels[i], counts[i])
		}
		return " " + labels[i] + " "
	}
	col := func(byteIdx int) int { return lw + runewidth.StringWidth(plain[:byteIdx]) }

	at := strings.Index(plain, seg(active))
	if at < 0 {
		return label + bar, nil
	}
	hits := []tabHit{{active, col(at), runewidth.StringWidth(seg(active))}}
	for end, j := at+len(seg(active)), active+1; j < len(labels); j++ { // right neighbours
		sg := seg(j)
		if !strings.HasPrefix(plain[end:], " "+sg) {
			break
		}
		hits = append(hits, tabHit{j, col(end + 1), runewidth.StringWidth(sg)})
		end += 1 + len(sg)
	}
	for start, j := at, active-1; j >= 0; j-- { // left neighbours
		sg := seg(j)
		if !strings.HasSuffix(plain[:start], sg+" ") {
			break
		}
		start -= 1 + len(sg)
		hits = append(hits, tabHit{j, col(start), runewidth.StringWidth(sg)})
	}
	return label + bar, hits
}

func (m Model) renderTabRow1(w int) string {
	bar, _ := m.tabBar(w)
	return bar
}

// subTabPrefix renders the "<Parent> › " lead-in of the Folders row — naming
// the parent explicitly so it's unambiguous which notebook the chips belong
// to even when the Notebooks row has scrolled and the parent isn't visible.
func subTabPrefix(top string) string {
	return styleTabParentRef.Render(top) + styleMuted.Render(" › ")
}

// folderBar draws the Folders row — the active top-level notebook's children
// as plain chips (current one accent, others dimmed) — within w columns, and
// the column span of each visible chip. "" when there is no Folders row.
// Overflow just truncates with an ellipsis: sub-notebook counts are small, so
// no second scroll mechanism.
func (m Model) folderBar(w int) (string, []tabHit) {
	if !m.hasFolderRow() {
		return "", nil
	}
	pos := m.currentPos()
	top := m.topFolders[pos.top-1]
	label := ""
	if lw := m.rowLabelW(); lw > 0 {
		label = rowLabel("Folders")
	}
	if label != "" {
		label += " " // the Notebooks pills are padded by one cell; line the chips up under their text
	}
	prefix := subTabPrefix(m.topFolderLabel(pos.top - 1))
	x := lipgloss.Width(label) + lipgloss.Width(prefix)
	var b strings.Builder
	var hits []tabHit
	for i, l := range m.subLabels(top) {
		style := styleSubInact
		if i == pos.sub {
			style = styleSubActive
		}
		r := style.Render(l)
		ww := lipgloss.Width(r)
		sep := 0
		if i > 0 {
			sep = 2
		}
		if x+sep+ww > w && len(hits) > 0 {
			b.WriteString(styleMuted.Render("  …"))
			break
		}
		b.WriteString(strings.Repeat(" ", sep))
		x += sep
		hits = append(hits, tabHit{i, x, ww})
		b.WriteString(r)
		x += ww
	}
	return ansi.Truncate(label+prefix+b.String(), w, "…"), hits
}

func (m Model) renderTabRow2(w int) string {
	row, _ := m.folderBar(w)
	return row
}

// tabHitTest returns which tab a mouse click landed on: row 0 for the
// Notebooks row (index into m.topFolders+1, "All" is 0), row 1 for the
// Folders row (index into the active notebook's children). row is -1 if the
// click missed both. Both rows are drawn after a 1-column margin.
func (m Model) tabHitTest(x, y int) (row, idx int) {
	ch := m.chrome()
	w := m.width - 1
	hit := func(hits []tabHit) int {
		for _, h := range hits {
			if x >= 1+h.x && x < 1+h.x+h.w {
				return h.idx
			}
		}
		return -1
	}
	switch {
	case y == ch.tabsY:
		_, hits := m.tabBar(w)
		if i := hit(hits); i >= 0 {
			return 0, i
		}
	case ch.foldersY >= 0 && y == ch.foldersY:
		_, hits := m.folderBar(w)
		if i := hit(hits); i >= 0 {
			return 1, i
		}
	}
	return -1, -1
}
