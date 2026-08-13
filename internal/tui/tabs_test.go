package tui

import (
	"testing"

	"github.com/aeon022/notectl/internal/models"
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

// Regression test for the real collision on this machine: a note's account
// disagreement between a long Exchange UPN and a shorter account name used
// to blow the header's width budget and get silently swallowed by the
// terminal's line wrap on the very next redraw (looked like the label
// flashing and disappearing). notebookAccountIndicator must now never
// return a label wider than the budget it's given, for any budget.
func TestNotebookAccountIndicator_NeverExceedsWidthBudget(t *testing.T) {
	longAccount := "2330069032@hochschule-burgenland.at"
	shortAccount := "Die Brücke || Gerwin"
	m := Model{
		accounts:   []string{longAccount, shortAccount, "iCloud"},
		topFolders: []string{"Notizen"},
		tabCursor:  1, // pos{top:1,sub:-1} -> activeFolder() == "Notizen"
		notes: []models.Note{
			{Account: longAccount, Folder: "Notizen"},
			{Account: shortAccount, Folder: "Notizen"},
		},
	}
	for _, budget := range []int{4, 10, 20, 40, 60, 100} {
		label, mixed := m.notebookAccountIndicator(budget)
		if got := runewidth.StringWidth(label); got > budget {
			t.Errorf("budget %d: label %q rendered at %d columns, exceeds budget", budget, label, got)
		}
		if label != "" && !mixed {
			t.Errorf("budget %d: label %q should be flagged mixed for a real 2-account collision", budget, label)
		}
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
