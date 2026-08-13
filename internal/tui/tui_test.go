package tui

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aeon022/notectl/internal/models"
	"github.com/aeon022/notectl/internal/store"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"github.com/spf13/viper"
)

// withAppleSource temporarily makes config.Source() return SourceApple,
// restoring the previous value afterwards — the Apple-bullet-vs-checklist
// disambiguation this file tests for only applies to that source.
func withAppleSource(t *testing.T) {
	t.Helper()
	prev := viper.GetString("source")
	viper.Set("source", "apple")
	t.Cleanup(func() { viper.Set("source", prev) })
}

func TestChecklistLookup_RealStateOverridesFakeCheckbox(t *testing.T) {
	withAppleSource(t)
	t.Cleanup(func() { currentChecklistState = nil })

	currentChecklistState = map[string]bool{
		"Item One": false,
		"Item Two": true,
	}

	// A confirmed checklist item renders with the real done state...
	if got := renderMDLine("• Item One", 80); !strings.Contains(got, "☐") {
		t.Errorf("unchecked real checklist item should show ☐, got %q", got)
	}
	if got := renderMDLine("• Item Two", 80); !strings.Contains(got, "☑") {
		t.Errorf("checked real checklist item should show ☑, got %q", got)
	}
	// ...but a bullet that isn't in the real checklist state (i.e. a genuine
	// plain bullet Apple Notes never marked as a checklist paragraph) must
	// NOT be faked into a checkbox — that fake-☐-for-everything behavior is
	// exactly the bug this file fixes.
	if got := renderMDLine("• Not a checklist item", 80); strings.Contains(got, "☐") || strings.Contains(got, "☑") {
		t.Errorf("plain (non-checklist) Apple bullet should render as a plain bullet, not a checkbox: %q", got)
	}
}

func TestNextNonBlankLine(t *testing.T) {
	lines := []string{"Header", "", "Item A", "Item B", "", "", "Item C", ""}
	//                    0      1     2         3      4   5     6      7

	tests := []struct {
		name string
		from int
		dir  int
		want int
	}{
		{"down over single blank", 0, 1, 2},
		{"down over double blank", 3, 1, 6},
		{"up over single blank", 2, -1, 0},
		{"up over double blank", 6, -1, 3},
		{"down at trailing blank stays put", 6, 1, 6}, // only blank lines remain after Item C
		{"up at top stays put", 0, -1, 0},
		{"down one step no blank between", 2, 1, 3},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := nextNonBlankLine(lines, tc.from, tc.dir)
			if got != tc.want {
				t.Errorf("nextNonBlankLine(from=%d, dir=%d) = %d, want %d", tc.from, tc.dir, got, tc.want)
			}
		})
	}
}

func TestChecklistLookup_UnknownStateFallsBackToPlainBullet(t *testing.T) {
	withAppleSource(t)
	t.Cleanup(func() { currentChecklistState = nil })
	currentChecklistState = nil // e.g. SQLite read failed / not yet loaded

	got := renderMDLine("• Some item", 80)
	if strings.Contains(got, "☐") || strings.Contains(got, "☑") {
		t.Errorf("with no checklist state available, bullets must not guess a checkbox state: %q", got)
	}
}

func TestToggleCheckboxLine(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"• item", "☑ item"},
		{"- item", "☑ item"},
		{"* item", "☑ item"},
		{"- [ ] open task", "- [x] open task"},
		{"* [ ] open task", "* [x] open task"},
		{"- [x] done task", "- [ ] done task"},
		{"- [X] done task", "- [ ] done task"},
		{"☑ checked item", "☐ checked item"},
		{"☐ unchecked item", "☑ unchecked item"},
		{"  • indented item", "  ☑ indented item"},
		{"\t- [ ] tab task", "\t- [x] tab task"},
		{"\t- [x] tab done", "\t- [ ] tab done"},
		{"  ☑ indented checked", "  ☐ indented checked"},
		{"  ☐ indented open", "  ☑ indented open"},
		{"normal text", "normal text"},
	}

	for _, tc := range tests {
		got := toggleCheckboxLine(tc.in)
		if got != tc.want {
			t.Errorf("toggleCheckboxLine(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestRenderMDLineCheckboxes(t *testing.T) {
	tests := []struct {
		in          string
		wantContain string
	}{
		{"- [ ] open", "☐ "},
		{"- [x] done", "☑ "},
		{"  - [ ] indented", "  "},
		{"  ☑ done box", "  "},
		{"  ☐ open box", "  "},
	}

	for _, tc := range tests {
		got := renderMDLine(tc.in, 80)
		if !strings.Contains(got, tc.wantContain) {
			t.Errorf("renderMDLine(%q) = %q, want it to contain %q", tc.in, got, tc.wantContain)
		}
	}
}

func TestPreprocessAndRenderMarkdownTables(t *testing.T) {
	raw := `Some header text
| Header 1 | Header 2 |
| --- | --- |
| Cell 1 | Cell 2 with very long text that should be capped when width is narrow |`

	out := renderMarkdown(raw, 50)
	if !strings.Contains(out, "│") || !strings.Contains(out, "├") {
		t.Errorf("renderMarkdown did not format table into graphical boxes: %s", out)
	}

	lines := preprocessMarkdownTables(strings.Split(raw, "\n"), 50)
	for _, l := range lines {
		if strings.HasPrefix(l, "│") {
			// Check that no single preprocessed table line exceeds width
			if len([]rune(l)) > 55 {
				t.Errorf("preprocessed table line exceeds bounds: %q", l)
			}
		}
	}
}

func TestRenderScrollbarAlignsGlyphColumn(t *testing.T) {
	vp := viewport.New(20, 5)
	// Content with very different line lengths, and more lines than the
	// viewport height so the scrollbar thumb/track actually renders.
	vp.SetContent("a\nbb\nccccccccccccccccc\nd\nee\nfff\ng")

	out := renderScrollbar(vp, "")
	lines := strings.Split(out, "\n")
	if len(lines) == 0 {
		t.Fatal("expected rendered lines, got none")
	}

	glyphCol := -1
	for i, l := range lines {
		// The glyph is the last rune of each rendered line (track "│" or
		// thumb "█", both single-width). Its byte-rune column should be
		// identical across every line regardless of that line's own text
		// length — a mismatch means the glyph isn't forming a straight bar.
		col := len([]rune(l)) - 1
		if glyphCol == -1 {
			glyphCol = col
			continue
		}
		if col != glyphCol {
			t.Errorf("line %d: glyph at column %d, want %d (same as other lines) — scrollbar not vertically aligned: %q", i, col, glyphCol, l)
		}
	}
}

func TestRenderScrollbarAlignsGlyphColumn_VariationSelectorEmoji(t *testing.T) {
	// Regression test for a real note: "🛏️" (bed + U+FE0F variation
	// selector) is measured as one column wider by lipgloss.Width than it
	// actually renders, which — when that measurement was used to pad this
	// line before appending the scrollbar glyph — threw just this line's
	// glyph one column out of alignment with every other row (looked like
	// a notch bulging out of the scrollbar).
	vp := viewport.New(20, 4)
	vp.SetContent("plain line one\n🛏️ Schlafen\nplain line three\nplain line four\nplain line five\nplain line six")

	out := renderScrollbar(vp, "")
	lines := strings.Split(out, "\n")

	glyphCol := -1
	for i, l := range lines {
		col := len([]rune(l)) - 1
		if glyphCol == -1 {
			glyphCol = col
			continue
		}
		if col != glyphCol {
			t.Errorf("line %d (%q): glyph at column %d, want %d", i, l, col, glyphCol)
		}
	}
}

func TestCommandPalette_TypeFilterAndExecute(t *testing.T) {
	m := New("")
	m.width, m.height = 100, 30

	mi, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(":")})
	m = mi.(Model)
	if !m.inPalette {
		t.Fatal("expected inPalette after ':'")
	}

	for _, r := range "sort" {
		mi, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = mi.(Model)
	}
	wasSortByDate := m.sortByDate

	mi, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = mi.(Model)
	if m.inPalette {
		t.Error("expected palette to close after executing a command")
	}
	if m.sortByDate == wasSortByDate {
		t.Error("expected 'sort' command to replay 'S' and flip sortByDate")
	}
}

func TestCommandPalette_EscCloses(t *testing.T) {
	m := New("")
	m.width, m.height = 100, 30
	mi, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(":")})
	m = mi.(Model)

	mi, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = mi.(Model)
	if m.inPalette {
		t.Error("expected esc to close the palette")
	}
}

func TestHelpOverlay_OpenScrollClose(t *testing.T) {
	m := Model{width: 100, height: 30}

	mi, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
	m = mi.(Model)
	if m.view != viewHelp {
		t.Fatalf("expected viewHelp after '?', got %v", m.view)
	}
	if m.helpVP.TotalLineCount() == 0 {
		t.Fatal("expected help content to be populated")
	}

	before := m.helpVP.ScrollPercent()
	for i := 0; i < 5; i++ {
		mi, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
		m = mi.(Model)
	}
	if m.helpVP.ScrollPercent() <= before {
		t.Errorf("expected scroll to advance after pressing j, stayed at %v", before)
	}

	mi, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = mi.(Model)
	if m.view != viewList {
		t.Errorf("expected esc to close help back to viewList, got %v", m.view)
	}
}

func TestHelpOverlay_FitsWithinBackgroundHeight(t *testing.T) {
	m := Model{width: 100, height: 30}
	m = m.openHelp()
	bgLines := len(strings.Split(m.renderList(), "\n"))
	if m.helpPopH > bgLines {
		t.Errorf("popup height %d exceeds background height %d", m.helpPopH, bgLines)
	}
}

func TestFilterNotes_FuzzyMatchesTitle(t *testing.T) {
	notes := []models.Note{
		{ID: "1", Title: "budgetctl release"},
		{ID: "2", Title: "unrelated"},
	}
	got := filterNotes(notes, "bgt")
	if len(got) != 1 || got[0].ID != "1" {
		t.Errorf("expected fuzzy 'bgt' to match only the title, got %+v", got)
	}
}

func TestFilterNotes_FallsBackToBodyAndTagsSubstring(t *testing.T) {
	notes := []models.Note{
		{ID: "1", Title: "unrelated", Body: "mentions budgetctl in passing"},
		{ID: "2", Title: "also unrelated", Tags: []string{"budgetctl"}},
		{ID: "3", Title: "no match", Body: "nothing here"},
	}
	got := filterNotes(notes, "budgetctl")
	if len(got) != 2 {
		t.Errorf("expected body and tag substring matches to keep 2 notes, got %d: %+v", len(got), got)
	}
}

func TestFilterNotes_DoesNotFuzzyMatchAcrossTheWholeBody(t *testing.T) {
	// Regression guard for the design decision (same as diaryctl): fuzzy
	// must only apply to the short title field, not the long free-form
	// body — otherwise almost any short query would find SOME subsequence
	// across a full body and over-match everything.
	notes := []models.Note{
		{ID: "1", Title: "short", Body: "a big beach trip yesterday"},
	}
	// "bibetr" is a subsequence of the whole body but not a literal
	// substring, and not a fuzzy match of the short title either.
	got := filterNotes(notes, "bibetr")
	if len(got) != 0 {
		t.Errorf("expected body text to NOT be fuzzy-matched as a whole, got %+v", got)
	}
}

func TestFilterNotes_EmptyQueryReturnsAllUnfiltered(t *testing.T) {
	notes := []models.Note{{ID: "1"}, {ID: "2"}}
	got := filterNotes(notes, "")
	if len(got) != 2 {
		t.Errorf("expected empty query to return all notes, got %d", len(got))
	}
}

func TestSearchMode_FiltersLiveAsUserTypes(t *testing.T) {
	m := New("")
	m.width, m.height = 100, 30
	notes := []models.Note{
		{ID: "1", Title: "budgetctl release", ModTime: time.Now()},
		{ID: "2", Title: "unrelated note", ModTime: time.Now()},
	}
	m.allNotes, m.notes = notes, notes

	mi, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	m = mi.(Model)
	if !m.searching {
		t.Fatal("expected '/' to enter search mode")
	}
	mi, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b")})
	m = mi.(Model)
	mi, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("g")})
	m = mi.(Model)
	mi, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("t")})
	m = mi.(Model)

	if len(m.notes) != 1 || m.notes[0].ID != "1" {
		t.Errorf("expected live filtering while typing (no enter needed), got %+v", m.notes)
	}
	if m.searchQ != "bgt" {
		t.Errorf("expected searchQ to track the live input, got %q", m.searchQ)
	}
}

func TestFormatNoteRow_SelectedBackgroundSpansFullWidth(t *testing.T) {
	// Regression test: formatNoteRow used to be built plain, then the
	// WHOLE composed row was wrapped in one outer styleSelected.Width(w).
	// Render() call at the caller. dateStyled/meta below carry their own
	// independent colors, and each one's Render() ends with a full SGR
	// reset — which clobbered the outer style for everything after it
	// (same bug class found and fixed in mailctl). Confirmed empirically
	// with a forced ANSI profile that the selected background did not
	// extend past the date column. Now applied per-segment instead.
	lipgloss.SetColorProfile(termenv.ANSI256)
	defer lipgloss.SetColorProfile(termenv.Ascii)

	n := models.Note{Title: "hello", ModTime: time.Now()}
	row := formatNoteRow(&n, 60, styleSelected, "")
	if lipgloss.Width(row) != 60 {
		t.Errorf("expected the rendered row to be exactly 60 columns wide, got %d", lipgloss.Width(row))
	}

	openCode := strings.SplitN(styleSelected.Render("x"), "x", 2)[0]
	lastOpen := strings.LastIndex(row, openCode)
	if lastOpen == -1 {
		t.Fatal("expected to find the selected style's escape code in the row at all")
	}
	after := strings.TrimSuffix(row[lastOpen+len(openCode):], "\x1b[0m")
	if after == "" {
		t.Error("expected trailing padding spaces after the last styled segment")
	}
	if strings.TrimSpace(after) != "" {
		t.Errorf("expected only whitespace (padding) after the last styled segment, got %q", after)
	}
}

func TestFormatNoteRow_LongFolderNeverOverflowsWidth(t *testing.T) {
	// Regression test: titleW used to be floored to 6 *after* subtracting
	// the full, untruncated meta (folder+tag) width — so a long folder
	// name on a narrow terminal could leave titleW at its floor while
	// meta itself still ran arbitrarily wide, pushing the whole row past
	// `width`. In the two-pane list view that overflow shifted the "│"
	// divider and everything right of it, breaking the layout (reported
	// against a long-titled note in a deeply nested folder on a narrow
	// terminal).
	n := models.Note{
		Title:   "hello",
		Folder:  "Change-Management/Some/Very/Long/Nested/Folder/Path/That/Goes/On",
		Tags:    []string{"a-fairly-long-tag-name-too"},
		ModTime: time.Now(),
	}
	for _, w := range []int{40, 60, 100} {
		row := formatNoteRow(&n, w, styleSelected, "")
		if got := lipgloss.Width(row); got != w {
			t.Errorf("width %d: expected rendered row to be exactly %d columns, got %d", w, w, got)
		}
	}
}

// Regression test for a real, live-reproduced bug: landing on a collision-
// split tab (e.g. one of two accounts' own "Notizen") set loadNotesCmd's
// account parameter to that tab's bound account, and since the tab-tree
// rebuild in the notesLoadedMsg handler branched on that same parameter,
// the ENTIRE row-1 tab bar collapsed down to just that one account's own
// folders — confirmed live via screenshots: "All accounts (3)" still shown
// in the header while row 1 silently shrank from 4+ notebooks to just
// "All" + "Notizen". loadNotesCmd now takes globalAccount as a separate
// parameter (the "["/"]" filter's own state) specifically so the tree
// choice stays keyed on that, not on whichever account happens to be
// filtering the note list — this pins that a bound tab's account (account)
// no longer collapses the tree as long as globalAccount is still "".
func TestLoadNotesCmd_BoundTabAccountDoesNotCollapseAccountAwareTree(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "notectl.db")
	viper.Set("db_path", dbPath)
	t.Cleanup(func() { viper.Set("db_path", "") })

	s, err := store.New(dbPath, false)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	now := time.Now()
	for _, n := range []models.Note{
		{ID: "apple-1", Title: "FH note", Folder: "Notizen", Account: "FH Burgenland", Source: "apple", ModTime: now, Created: now},
		{ID: "apple-2", Title: "Brücke note", Folder: "Notizen", Account: "Die Brücke", Source: "apple", ModTime: now, Created: now},
		{ID: "apple-3", Title: "Baby note", Folder: "Baby", Account: "FH Burgenland", Source: "apple", ModTime: now, Created: now},
	} {
		n := n
		if err := s.Upsert(t.Context(), &n); err != nil {
			t.Fatalf("Upsert: %v", err)
		}
	}
	s.Close()

	// Simulates being on the "Notizen (FH Burgenland)" collision-split tab
	// (account = its bound account) while still browsing "All accounts"
	// overall (globalAccount = "").
	msg := loadNotesCmd("FH Burgenland", "Notizen", "")()
	loaded, ok := msg.(notesLoadedMsg)
	if !ok {
		t.Fatalf("loadNotesCmd() returned %T, want notesLoadedMsg", msg)
	}
	if loaded.folderInfoByAccount == nil {
		t.Fatal("folderInfoByAccount is nil — the tab tree would collapse to just the bound account's own folders instead of staying account-aware")
	}
	if _, ok := loaded.folderInfoByAccount["Die Brücke"]; !ok {
		t.Error(`folderInfoByAccount missing "Die Brücke" — the other account's notebooks disappeared from the tree`)
	}
	if len(loaded.notes) != 1 || loaded.notes[0].Title != "FH note" {
		t.Errorf("notes = %v, want exactly the one note actually in the bound tab (FH Burgenland/Notizen)", loaded.notes)
	}
}

// drainCmd runs a tea.Cmd and feeds every message it produces back into
// Update, recursively, so a chain of Cmd -> Msg -> another Cmd (exactly
// what tab/shift+tab triggers: a key press returns loadNotesCmd, which
// resolves to notesLoadedMsg, which the Update case for it handles without
// itself returning another Cmd here) settles completely before the caller
// inspects the resulting Model — matching what actually happens once
// bubbletea's real runtime loop executes a Cmd, not just what a single
// Update call alone would show.
func drainCmd(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	for cmd != nil {
		msg := cmd()
		if msg == nil {
			return m
		}
		var mi tea.Model
		mi, cmd = m.Update(msg)
		m = mi.(Model)
	}
	return m
}

// End-to-end regression test for the real, screenshotted bug: walking
// tab/shift+tab through the actual app (not just calling loadNotesCmd in
// isolation) against real per-account folder data reproducing the live
// collision (two accounts each with their own "Notizen", one of them
// currently empty). Drives the real Model.Update loop — boot, then every
// forward tab step, then every step back — and asserts the row-1 tab count
// never changes mid-walk. It's supposed to stay exactly constant the whole
// time; the live bug made it collapse from 6 notebooks down to 1 the
// moment the walk landed on a collision-split tab, then partially and
// incorrectly "recover" on the way back — both visible in the screenshots
// that reported this.
func TestFullAppWalk_TabTreeNeverCollapsesMidWalk(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "notectl.db")
	viper.Set("db_path", dbPath)
	t.Cleanup(func() { viper.Set("db_path", "") })

	s, err := store.New(dbPath, false)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	now := time.Now()
	for _, n := range []models.Note{
		{ID: "apple-1", Title: "Baby note", Folder: "Baby", Account: "FH Burgenland", Source: "apple", ModTime: now, Created: now},
		{ID: "apple-2", Title: "CM note", Folder: "Change-Management", Account: "FH Burgenland", Source: "apple", ModTime: now, Created: now},
		{ID: "apple-3", Title: "Notes note", Folder: "Notes", Account: "FH Burgenland", Source: "apple", ModTime: now, Created: now},
		{ID: "apple-4", Title: "FH Notizen", Folder: "Notizen", Account: "FH Burgenland", Source: "apple", ModTime: now, Created: now},
		{ID: "apple-5", Title: "Brücke Notizen", Folder: "Notizen", Account: "Die Brücke", Source: "apple", ModTime: now, Created: now},
	} {
		n := n
		if err := s.Upsert(t.Context(), &n); err != nil {
			t.Fatalf("Upsert: %v", err)
		}
	}
	if err := s.ReplaceFolders(t.Context(), "apple", map[string][]string{
		"FH Burgenland": {"Baby", "Change-Management", "Notes", "Notizen"},
		"Die Brücke":    {"Notizen"},
	}); err != nil {
		t.Fatalf("ReplaceFolders: %v", err)
	}
	s.Close()

	m := Model{width: 120, height: 40, sortByDate: true, hoverRow: -1, lastClickRow: -1}
	m = drainCmd(t, m, loadNotesCmd("", "", ""))

	const wantTops = 5 // Baby, Change-Management, Notes, Notizen(FH), Notizen(Brücke)
	if len(m.topFolders) != wantTops {
		t.Fatalf("after boot: len(topFolders) = %d, want %d — %v", len(m.topFolders), wantTops, m.topFolders)
	}

	n := len(m.tabPositions())
	for step := 0; step < n; step++ {
		mi, cmd := m.Update(tea.KeyMsg{Type: tea.KeyTab})
		m = drainCmd(t, mi.(Model), cmd)
		if len(m.topFolders) != wantTops {
			t.Fatalf("forward step %d (tabCursor=%d): len(topFolders) = %d, want %d — tab tree collapsed mid-walk: %v",
				step, m.tabCursor, len(m.topFolders), wantTops, m.topFolders)
		}
		assertNotesMatchActiveTab(t, m, "forward", step)
	}
	for step := 0; step < n; step++ {
		mi, cmd := m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
		m = drainCmd(t, mi.(Model), cmd)
		if len(m.topFolders) != wantTops {
			t.Fatalf("backward step %d (tabCursor=%d): len(topFolders) = %d, want %d — tab tree collapsed mid-walk: %v",
				step, m.tabCursor, len(m.topFolders), wantTops, m.topFolders)
		}
		assertNotesMatchActiveTab(t, m, "backward", step)
	}
}

// assertNotesMatchActiveTab checks that every note currently loaded
// actually belongs to the tab that's supposedly selected — the second
// symptom visible in the real screenshots alongside the tab tree
// collapsing: after navigating back, the cursor landed on one notebook
// (e.g. "Baby") while the note list still showed a note from a completely
// different one ("Notizen"), a stale-content mismatch left over from the
// tree reshaping mid-walk.
func assertNotesMatchActiveTab(t *testing.T, m Model, dir string, step int) {
	t.Helper()
	folder := m.activeFolder()
	account := m.effectiveAccount()
	for _, note := range m.notes {
		if folder != "" && note.Folder != folder && !strings.HasPrefix(note.Folder, folder+"/") {
			t.Fatalf("%s step %d: note %q has folder %q, want %q (or a descendant) — stale/mismatched note list", dir, step, note.Title, note.Folder, folder)
		}
		if account != "" && note.Account != account {
			t.Fatalf("%s step %d: note %q has account %q, want %q — stale/mismatched note list", dir, step, note.Title, note.Account, account)
		}
	}
}

func TestHighlightMatches_ColorsOnlyMatchedRunes(t *testing.T) {
	lipgloss.SetColorProfile(termenv.ANSI256)
	defer lipgloss.SetColorProfile(termenv.Ascii)

	idxs := fuzzyMatchIndexes("bgt", "budgetctl")
	if idxs == nil {
		t.Fatal("expected 'bgt' to fuzzy-match 'budgetctl'")
	}
	base := lipgloss.NewStyle()
	out := highlightMatches("budgetctl", idxs, base)
	if out == base.Render("budgetctl") {
		t.Error("expected highlightMatches to differ from a plain render for a real match")
	}
}
