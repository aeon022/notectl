package tui

import (
	"strings"
	"testing"

	"github.com/aeon022/notectl/internal/store"
	"github.com/mattn/go-runewidth"
)

func TestHasMultipleAccounts(t *testing.T) {
	cases := []struct {
		accounts []string
		want     bool
	}{
		{nil, false},
		{[]string{"iCloud"}, false},
		{[]string{"iCloud", "FH Burgenland"}, true},
	}
	for _, c := range cases {
		m := Model{accounts: c.accounts}
		if got := m.hasMultipleAccounts(); got != c.want {
			t.Errorf("hasMultipleAccounts() with accounts=%v = %v, want %v", c.accounts, got, c.want)
		}
	}
}

func TestActiveAccount(t *testing.T) {
	m := Model{accounts: []string{"FH Burgenland", "iCloud"}}

	if got := m.activeAccount(); got != "" {
		t.Errorf("cursor 0 (default) should be \"All accounts\" (\"\"), got %q", got)
	}

	m.accountCursor = 1
	if got := m.activeAccount(); got != "FH Burgenland" {
		t.Errorf("cursor 1 = %q, want %q", got, "FH Burgenland")
	}

	m.accountCursor = 2
	if got := m.activeAccount(); got != "iCloud" {
		t.Errorf("cursor 2 = %q, want %q", got, "iCloud")
	}

	// Out of range (e.g. an account disappeared after a sync) falls back
	// to "All accounts" rather than panicking on the slice index.
	m.accountCursor = 99
	if got := m.activeAccount(); got != "" {
		t.Errorf("out-of-range cursor should fall back to \"\", got %q", got)
	}
	m.accountCursor = -1
	if got := m.activeAccount(); got != "" {
		t.Errorf("negative cursor should fall back to \"\", got %q", got)
	}
}

func TestResolveAccountCursor(t *testing.T) {
	m := Model{accounts: []string{"FH Burgenland", "iCloud"}}

	got, ok := m.resolveAccountCursor("iCloud")
	if !ok || got != 2 {
		t.Errorf("resolveAccountCursor(iCloud) = (%d, %v), want (2, true)", got, ok)
	}

	got, ok = m.resolveAccountCursor("Gmail")
	if ok || got != 0 {
		t.Errorf("resolveAccountCursor(Gmail) (unknown) = (%d, %v), want (0, false)", got, ok)
	}
}

func TestAccountIndicator(t *testing.T) {
	m := Model{accounts: []string{"FH Burgenland", "iCloud"}}

	if got := m.accountIndicator(); got != "All accounts (2)" {
		t.Errorf("accountIndicator() at cursor 0 = %q, want %q", got, "All accounts (2)")
	}

	m.accountCursor = 2
	if got := m.accountIndicator(); got != "iCloud (2/2)" {
		t.Errorf("accountIndicator() at cursor 2 = %q, want %q", got, "iCloud (2/2)")
	}

	// A single account has nothing to disambiguate, so there's no
	// indicator to show at all — not even "All accounts (1)".
	single := Model{accounts: []string{"iCloud"}}
	if got := single.accountIndicator(); got != "" {
		t.Errorf("accountIndicator() with one account = %q, want \"\"", got)
	}
}

// renderAccountLine replaced the old header-embedded account indicator,
// whose fixed, shared-with-"notectl"-and-the-date width budget was exactly
// what caused a real account name (a 36-char Exchange UPN, seen live) to
// silently overflow the line and get overwritten by the next redraw. Its
// own full-width line removes that budget pressure, but must still never
// itself exceed whatever width it's actually given (a narrow terminal, a
// resize mid-render) — this pins that, plus that a tab bound to a specific
// account (see topFolderAccounts) names that account, not the global
// "["/"]" filter state.
func TestRenderAccountLine_NamesBoundAccountAndNeverExceedsWidth(t *testing.T) {
	longAccount := "2330069032@hochschule-burgenland.at"
	m := Model{
		accounts:          []string{longAccount, "Die Brücke || Gerwin", "iCloud"},
		topFolders:        []string{"Notizen"},
		topFolderAccounts: []string{longAccount},
		tabCursor:         1, // pos{top:1,sub:-1} -> activeFolderAccount() == longAccount
	}
	for _, w := range []int{4, 10, 20, 40, 60, 100} {
		line := m.renderAccountLine(w)
		if got := runewidth.StringWidth(line); got > w {
			t.Errorf("width %d: line %q rendered at %d columns, exceeds width", w, line, got)
		}
	}
	if line := m.renderAccountLine(200); !strings.Contains(line, longAccount) {
		t.Errorf("renderAccountLine(200) = %q, want it to name the bound account %q", line, longAccount)
	}
}

// A collision between two accounts' identically-named top-level folders
// (the real-world case this whole feature exists for — e.g. two accounts
// both defaulting to a folder called "Notizen") must not merge under
// buildFolderTree once notes are scoped to one account's own folder list
// before it's called — this documents that buildFolderTree itself is
// account-agnostic by design (see loadNotesCmd, which calls
// ListFolderInfoByAccount to pre-scope the input for a single selected
// account rather than teaching this function about accounts;
// buildAccountAwareFolderTree is the "All accounts" sibling that does need
// to know about them, see its own doc comment).
func TestBuildFolderTree_ScopedInputAvoidsCollision(t *testing.T) {
	// Only "Notizen" from the active account should ever reach here.
	tops, children := buildFolderTree([]string{"Notizen", "Projects/Git"})
	if len(tops) != 2 || tops[0] != "Notizen" || tops[1] != "Projects" {
		t.Errorf("tops = %v, want [Notizen Projects]", tops)
	}
	if got := children["Projects"]; len(got) != 1 || got[0] != "Projects/Git" {
		t.Errorf("children[Projects] = %v, want [Projects/Git]", got)
	}
}

// The real-world case buildAccountAwareFolderTree exists for: two Apple
// Notes accounts each with their own "Notizen", one with content, one
// currently empty (only known via the folders table — see FolderInfo's doc
// comment). It must split into two separate tabs instead of merging them,
// non-empty account first.
func TestBuildAccountAwareFolderTree_SplitsCollidingTopLevelFolder(t *testing.T) {
	folders := []string{"Notizen", "Projects/Git"}
	counts := map[string]int{"Notizen": 5, "Projects": 2, "Projects/Git": 2}
	perAccount := map[string][]store.FolderInfo{
		"FH Burgenland": {{Account: "FH Burgenland", Folder: "Notizen", Count: 5}},
		"Die Brücke":    {{Account: "Die Brücke", Folder: "Notizen", Count: 0}},
	}

	tops, topAccounts, children := buildAccountAwareFolderTree(folders, counts, perAccount, false)

	if len(tops) != 3 {
		t.Fatalf("tops = %v, want 3 entries (Notizen x2 + Projects)", tops)
	}
	if tops[0] != "Notizen" || topAccounts[0] != "FH Burgenland" {
		t.Errorf("tops[0] = (%q, %q), want (Notizen, FH Burgenland) — non-empty account should sort first", tops[0], topAccounts[0])
	}
	if tops[1] != "Notizen" || topAccounts[1] != "Die Brücke" {
		t.Errorf("tops[1] = (%q, %q), want (Notizen, Die Brücke)", tops[1], topAccounts[1])
	}
	if tops[2] != "Projects" || topAccounts[2] != "" {
		t.Errorf("tops[2] = (%q, %q), want (Projects, \"\") — non-colliding folder stays account-agnostic", tops[2], topAccounts[2])
	}
	if got := children["Projects"]; len(got) != 1 || got[0] != "Projects/Git" {
		t.Errorf("children[Projects] = %v, want [Projects/Git] — untouched by the account split", got)
	}
	if got := counts["Notizen\x00FH Burgenland"]; got != 5 {
		t.Errorf("counts[Notizen\\x00FH Burgenland] = %d, want 5 (written by the split so topFolderKey can look it up)", got)
	}
	if got := counts["Notizen\x00Die Brücke"]; got != 0 {
		t.Errorf("counts[Notizen\\x00Die Brücke] = %d, want 0", got)
	}
}

// hideEmpty must drop an empty collision-split tab (the Brücke side, still
// at zero notes) while keeping its non-empty sibling — this is the state
// "H" toggles into and a note being written toggles back out of.
func TestBuildAccountAwareFolderTree_HideEmptyDropsEmptySplitTab(t *testing.T) {
	folders := []string{"Notizen"}
	counts := map[string]int{"Notizen": 5}
	perAccount := map[string][]store.FolderInfo{
		"FH Burgenland": {{Account: "FH Burgenland", Folder: "Notizen", Count: 5}},
		"Die Brücke":    {{Account: "Die Brücke", Folder: "Notizen", Count: 0}},
	}

	tops, topAccounts, _ := buildAccountAwareFolderTree(folders, counts, perAccount, true)

	if len(tops) != 1 || tops[0] != "Notizen" || topAccounts[0] != "FH Burgenland" {
		t.Errorf("tops/topAccounts = %v/%v, want exactly [Notizen]/[FH Burgenland] with the empty Brücke tab hidden", tops, topAccounts)
	}
}

// A collapsed top-level notebook's children must be entirely absent from
// tabPositions — not just skipped over — so tab/shift+tab never lands on
// one until it's been explicitly expanded via "right"/"l". This is the
// replacement for the old auto-reveal-on-select behavior that made
// tab/shift+tab always drag every notebook's children along with it.
func TestTabPositions_ChildrenOnlyReachableWhenExpanded(t *testing.T) {
	m := Model{
		topFolders: []string{"Projects"},
		subFolders: map[string][]string{"Projects": {"Projects/Git"}},
	}

	positions := m.tabPositions()
	if len(positions) != 2 {
		t.Fatalf("collapsed: tabPositions() = %v, want exactly [{0,-1} {1,-1}] (no child)", positions)
	}

	m.setExpanded(0, true)
	positions = m.tabPositions()
	if len(positions) != 3 || positions[2] != (tabPos{1, 0}) {
		t.Fatalf("expanded: tabPositions() = %v, want [{0,-1} {1,-1} {1,0}]", positions)
	}

	m.setExpanded(0, false)
	positions = m.tabPositions()
	if len(positions) != 2 {
		t.Errorf("re-collapsed: tabPositions() = %v, want the child gone again", positions)
	}
}

// topFolderLabel is the only place a parent notebook announces it has
// children at all (see the package doc comment) — must show "▸" collapsed,
// "▾" expanded, and no marker for a notebook with nothing underneath it.
func TestTopFolderLabel_DisclosureMarker(t *testing.T) {
	m := Model{
		topFolders: []string{"Projects", "Baby"},
		subFolders: map[string][]string{"Projects": {"Projects/Git"}},
	}

	if got := m.topFolderLabel(0); got != "▸ Projects" {
		t.Errorf("collapsed, has children: topFolderLabel(0) = %q, want %q", got, "▸ Projects")
	}
	if got := m.topFolderLabel(1); got != "Baby" {
		t.Errorf("no children: topFolderLabel(1) = %q, want plain %q (no marker)", got, "Baby")
	}

	m.setExpanded(0, true)
	if got := m.topFolderLabel(0); got != "▾ Projects" {
		t.Errorf("expanded: topFolderLabel(0) = %q, want %q", got, "▾ Projects")
	}
}

// Two collision-split tabs sharing a plain folder name (see
// topFolderAccounts) must expand independently — keyed by topFolderKey
// (folder+account composite), not just the folder name, or expanding one
// account's "Notizen" would also silently expand the other account's.
func TestSetExpanded_CollisionSplitTabsExpandIndependently(t *testing.T) {
	m := Model{
		topFolders:        []string{"Notizen", "Notizen"},
		topFolderAccounts: []string{"FH Burgenland", "Die Brücke"},
		subFolders:        map[string][]string{"Notizen": {"Notizen/Archiv"}},
	}

	m.setExpanded(0, true)
	if !m.isExpanded(0) {
		t.Error("isExpanded(0) = false after setExpanded(0, true)")
	}
	if m.isExpanded(1) {
		t.Error("isExpanded(1) = true — expanding tab 0 (FH Burgenland) should not expand tab 1 (Die Brücke)")
	}
}

// Regression test for a real, live-reproduced bug: an uncapped tab-name
// width let one long notebook name ("Change-Management", 17 columns) force
// tabWindow to evict two tabs for a single one-step tab/shift+tab move —
// press it once, watch two previously-visible tabs disappear together, not
// just the one that had to make room. topFolderNameWidth capping every
// name fixes this for realistic terminal widths (a genuinely tiny terminal,
// ~35 columns or less, can still only fit one tab at a time regardless —
// that's an accepted, unavoidable edge case, not what this pins).
func TestTabWindow_SingleStepNeverEvictsMoreThanOneTab(t *testing.T) {
	m := Model{
		topFolders:        []string{"Baby", "Change-Management", "Notes", "Notizen", "Notizen", "Projects"},
		topFolderAccounts: []string{"", "", "", "Die Brücke || Gerwin", "2330069032@hochschule-burgenland.at", ""},
		subFolders: map[string][]string{
			"Change-Management": {"Change-Management/KI"},
			"Projects":          {"Projects/Git", "Projects/MISSIONCTL", "Projects/QuantumPod", "Projects/Syncthing"},
		},
		folderCounts: map[string]int{
			"":                                45,
			"Baby":                            3,
			"Change-Management":               9,
			"Notes":                           23,
			"Notizen\x00Die Brücke || Gerwin": 2,
			"Notizen\x002330069032@hochschule-burgenland.at": 1,
			"Projects": 1,
		},
	}
	for _, width := range []int{50, 60, 70, 80, 100, 120} {
		m.width = width
		m.tabCursor = 0
		m.tabScroll = 0
		n := len(m.tabPositions())
		for step := 0; step < n; step++ {
			labels := m.topLabels()
			pos := m.currentPos()
			before := m.tabScroll
			start, _ := tabWindow(labels, pos.top, before, m.width-1)
			if delta := start - before; delta > 1 {
				t.Errorf("width=%d step=%d: scroll jumped from %d to %d (delta %d) on a single tab-step, evicting more than one tab at once", width, step, before, start, delta)
			}
			m.tabScroll = start
			m.tabCursor = (m.tabCursor + 1) % n
		}
	}
}
