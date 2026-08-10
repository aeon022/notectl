package tui

import "testing"

func TestShowAccountRow(t *testing.T) {
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
		if got := m.showAccountRow(); got != c.want {
			t.Errorf("showAccountRow() with accounts=%v = %v, want %v", c.accounts, got, c.want)
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

func TestAccountLabels(t *testing.T) {
	m := Model{
		accounts:      []string{"FH Burgenland", "iCloud"},
		accountCounts: map[string]int{"": 10, "FH Burgenland": 3, "iCloud": 7},
	}
	labels := m.accountLabels()
	want := []string{"All accounts 10", "FH Burgenland 3", "iCloud 7"}
	if len(labels) != len(want) {
		t.Fatalf("accountLabels() = %v, want %v", labels, want)
	}
	for i := range want {
		if labels[i] != want[i] {
			t.Errorf("accountLabels()[%d] = %q, want %q", i, labels[i], want[i])
		}
	}
}

// A collision between two accounts' identically-named top-level folders
// (the real-world case this whole feature exists for — e.g. two accounts
// both defaulting to a folder called "Notizen") must not merge under
// buildFolderTree once notes are scoped to one account's own folder list
// before it's called — this documents that buildFolderTree itself is
// account-agnostic by design (see loadNotesCmd, which calls
// ListFoldersByAccount to pre-scope the input rather than teaching this
// function about accounts).
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
