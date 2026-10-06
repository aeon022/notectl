package tui

import (
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/aeon022/missionctl-core/tuitest"
	"github.com/charmbracelet/x/ansi"
	runewidth "github.com/mattn/go-runewidth"
)

// ── tabs (ui.Tabs) ────────────────────────────────────────────────────────────

func tabsModel(width int) Model {
	return Model{
		width:      width,
		height:     30,
		accounts:   []string{"Die Brücke || Gerwin", "iCloud"},
		topFolders: []string{"Baby", "Change-Management", "Linux", "Projects", "Notes"},
		folderCounts: map[string]int{
			"": 90, "Baby": 5, "Change-Management": 23, "Linux": 1, "Projects": 40, "Notes": 12,
		},
	}
}

func TestTabBar_PillsWithCountsAndNoFixedWidthBoxes(t *testing.T) {
	m := tabsModel(160)
	bar, hits := m.tabBar(m.width - 1)
	plain := ansi.Strip(bar)
	for _, want := range []string{" All 90 ", " Baby 5 ", " Change-Management 23 ", " Linux 1 "} {
		if !strings.Contains(plain, want) {
			t.Errorf("tab bar %q missing %q (names are shown whole, not cut to a fixed box)", plain, want)
		}
	}
	if len(hits) != len(m.topFolders)+1 {
		t.Errorf("all tabs fit at 160 columns, got %d hits", len(hits))
	}
	if lipgloss.Width(bar) > m.width-1 {
		t.Errorf("bar %d wider than %d", lipgloss.Width(bar), m.width-1)
	}
}

func TestTabBar_ActiveStaysVisibleAndClickable(t *testing.T) {
	for _, width := range []int{50, 70, 100, 160} {
		for cursor := 0; cursor <= len(tabsModel(0).topFolders); cursor++ {
			m := tabsModel(width)
			m.tabCursor = cursor
			bar, hits := m.tabBar(m.width - 1)
			if lipgloss.Width(bar) > m.width-1 {
				t.Fatalf("w=%d cursor=%d: bar is %d wide, budget %d", width, cursor, lipgloss.Width(bar), m.width-1)
			}
			plain := ansi.Strip(bar)
			saw := false
			for _, h := range hits {
				cell := strings.TrimSpace(cellsAt(plain, h.x, h.w))
				label := m.topLabels()[h.idx]
				if !strings.HasPrefix(cell, label) {
					t.Errorf("w=%d cursor=%d: span of tab %d reads %q, want prefix %q", width, cursor, h.idx, cell, label)
				}
				// the row is drawn after a 1-column margin; a click in the middle of the span hits this tab on row 1 only
				if row, idx := m.tabHitTest(1+h.x+h.w/2, 1); row != 0 || idx != h.idx {
					t.Errorf("w=%d cursor=%d: click on tab %d resolved to (%d,%d)", width, cursor, h.idx, row, idx)
				}
				saw = saw || h.idx == cursor
			}
			if !saw {
				t.Errorf("w=%d: active tab %d must stay visible and clickable (hits %v)", width, cursor, hits)
			}
		}
	}
}

func TestTabHitTest_WithMultipleAccountsRowOneIsStillTheTabRow(t *testing.T) {
	// the account line used to push the tab row down to y=2 while hit-testing
	// kept using y=1; the account now lives in the header, so row 1 is the tabs
	m := tabsModel(120)
	_, hits := m.tabBar(m.width - 1)
	if row, _ := m.tabHitTest(1+hits[0].x+1, 1); row != 0 {
		t.Errorf("click on the first tab at y=1 must hit it, got row %d", row)
	}
	if row, _ := m.tabHitTest(1+hits[0].x+1, 0); row != -1 {
		t.Errorf("the header row is not clickable, got row %d", row)
	}
}

func TestPreambleHasNoAccountLineAnymore(t *testing.T) {
	m := tabsModel(120)
	if got := m.preambleRows(); got != 3 { // header + tabs + divider
		t.Errorf("preambleRows() = %d, want 3 (the account context lives in the header)", got)
	}
}

func TestTabBar_SyncSuffixSharesTheBudget(t *testing.T) {
	m := tabsModel(90)
	m.lastSynced = time.Now().Add(-20 * time.Hour)
	bar, hits := m.tabBar(m.width - 1)
	if lipgloss.Width(bar) > m.width-1 {
		t.Fatalf("bar+suffix %d > %d: %q", lipgloss.Width(bar), m.width-1, ansi.Strip(bar))
	}
	if !strings.Contains(ansi.Strip(bar), "synced 20h ago") {
		t.Errorf("sync status missing: %q", ansi.Strip(bar))
	}
	for _, h := range hits {
		if h.x+h.w > lipgloss.Width(bar) {
			t.Errorf("hit span %+v lies outside the drawn bar", h)
		}
	}
}

// ── one-line footer ───────────────────────────────────────────────────────────

func TestHelpBar_OneLineAndKeepsQuitAndHelpWhenNarrow(t *testing.T) {
	m := newNotes(t, sampleNotes()...)
	for _, w := range []int{30, 40, 60, 90, 140} {
		bar := m.renderHelpBar(w)
		if strings.Contains(bar, "\n") {
			t.Errorf("w=%d: footer must be ONE line, got %q", w, ansi.Strip(bar))
		}
		if lipgloss.Width(bar) > w {
			t.Errorf("w=%d: footer is %d wide", w, lipgloss.Width(bar))
		}
		if w >= 40 && !strings.Contains(ansi.Strip(bar), "? help") {
			t.Errorf("w=%d: ? is dropped last, got %q", w, ansi.Strip(bar))
		}
		if w >= 60 && !strings.Contains(ansi.Strip(bar), "q quit") {
			t.Errorf("w=%d: q is dropped late, got %q", w, ansi.Strip(bar))
		}
	}
	wide := ansi.Strip(m.renderHelpBar(200))
	for _, want := range []string{"enter open", "n new", "/ search", "tab notebook", "l/h expand", "1/3"} {
		if !strings.Contains(wide, want) {
			t.Errorf("wide footer missing %q: %q", want, wide)
		}
	}
	m.status = "Saved"
	if bar := m.renderHelpBar(80); strings.Contains(bar, "\n") || !strings.Contains(ansi.Strip(bar), "✓ Saved") {
		t.Errorf("status message stays on one line: %q", ansi.Strip(bar))
	}
}

func TestListFillsTheTerminalExactlyWithTheOneLineFooter(t *testing.T) {
	m := newNotes(t, sampleNotes()...)
	m.loading = false
	m.accounts = []string{"a", "b"}
	for _, size := range [][2]int{{90, 30}, {140, 40}, {60, 20}} {
		mi, _ := tuitest.Send(m, tuitest.Resize(size[0], size[1]))
		out := mi.(Model).renderList()
		if n := strings.Count(out, "\n") + 1; n > size[1] {
			t.Errorf("%dx%d: %d lines exceed the terminal", size[0], size[1], n)
		}
	}
}

// ── header context ────────────────────────────────────────────────────────────

func TestHeaderCarriesAccountContextInTheMiddle(t *testing.T) {
	m := tabsModel(120)
	h := ansi.Strip(m.renderAppHeader(m.width - 1))
	if !strings.Contains(h, "notectl") || !strings.Contains(h, "All accounts (2)") {
		t.Errorf("header = %q", h)
	}
	if strings.Index(h, "notectl") > strings.Index(h, "All accounts") || strings.Index(h, "All accounts") > strings.Index(h, time.Now().Format("2006")) {
		t.Errorf("order must be name · context · date: %q", h)
	}
}

// ── markdown: fenced code, headings ───────────────────────────────────────────

func TestRenderMarkdown_FencedCodeIsABlockNotMarkdown(t *testing.T) {
	body := "intro\n```go\nfmt.Println(\"hi\")\n# not a heading\n- not a bullet\n```\nafter"
	out := renderMarkdown(body, 40)
	plain := ansi.Strip(out)
	if strings.Contains(plain, "```") {
		t.Errorf("fence markers must not be shown:\n%s", plain)
	}
	for _, want := range []string{"fmt.Println(\"hi\")", "# not a heading", "- not a bullet", "go"} {
		if !strings.Contains(plain, want) {
			t.Errorf("code line must be shown verbatim (missing %q):\n%s", want, plain)
		}
	}
	// the code sits on its own surface: every code row carries the code background
	bg := strings.SplitN(styleMDCodeBlock.Render("x"), "x", 2)[0]
	rows := strings.Split(out, "\n")
	codeRows := 0
	for _, r := range rows {
		if strings.Contains(ansi.Strip(r), "fmt.Println") || strings.Contains(ansi.Strip(r), "# not a heading") {
			codeRows++
			if !strings.Contains(r, bg) {
				t.Errorf("code row lacks the code background: %q", r)
			}
			if lipgloss.Width(r) != 40 {
				t.Errorf("code rows are padded to the full width, got %d", lipgloss.Width(r))
			}
		}
		if strings.Contains(ansi.Strip(r), "intro") && strings.Contains(r, bg) {
			t.Errorf("ordinary text must not get the code background: %q", r)
		}
	}
	if codeRows != 2 {
		t.Errorf("expected 2 code rows, got %d", codeRows)
	}
	// "# not a heading" must not be styled as an H1
	if strings.Contains(out, styleMDH1.Render("not a heading")) {
		t.Error("a '# comment' inside a fence was rendered as a heading")
	}
}

func TestCodeStates(t *testing.T) {
	lines := []string{"a", "```", "b", "```py", "c", "```", "d", "x ```inline``` y", "```", "open until the end"}
	// fences toggle: line 3 "```py" while inside a block closes it, so "c" is plain again
	want := []codeState{notCode, fenceLine, codeLine, fenceLine, notCode, fenceLine, codeLine, codeLine, fenceLine, notCode}
	got := codeStates(lines)
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d (%q): state %d, want %d", i, lines[i], got[i], want[i])
		}
	}
	if st := codeStates([]string{"```", "never closed", "still code"}); st[2] != codeLine {
		t.Error("an unterminated fence runs to the end of the note")
	}
	if st := codeStates([]string{"```one-line```", "text"}); st[0] != notCode || st[1] != notCode {
		t.Errorf("```one-line``` is an inline span, not a fence: %v", st)
	}
}

func TestRenderMarkdown_HeadingsAreSetApartFromTheTextAbove(t *testing.T) {
	plain := ansi.Strip(renderMarkdown("intro\n# Title\nbody\n## Sub\nmore", 40))
	lines := strings.Split(strings.TrimRight(plain, "\n "), "\n")
	var got []string
	for _, l := range lines {
		got = append(got, strings.TrimSpace(l))
	}
	want := []string{"intro", "", "Title", "body", "", "Sub", "more"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("lines = %q, want %q", got, want)
	}
	// no extra blank line when there already is one, and none before a leading heading
	plain = ansi.Strip(renderMarkdown("# First\n\n## Second", 40))
	if strings.Contains(plain, "\n\n\n") || strings.HasPrefix(plain, "\n") {
		t.Errorf("no doubled/leading blank lines: %q", plain)
	}
}

func TestRenderDetailBody_FencedCodeKeepsOneRowPerSourceLine(t *testing.T) {
	// the detail view maps source lines to cursor rows 1:1, so code blocks may
	// restyle lines but must never add or remove rows
	body := "# T\n```sh\ncd ~/x\n# comment\n```\nend"
	out, vc := renderDetailBody(body, 3, 40)
	rows := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(rows) != 6 {
		t.Fatalf("expected 6 rows (one per source line), got %d:\n%s", len(rows), ansi.Strip(out))
	}
	if vc != 3 {
		t.Errorf("cursor row = %d, want 3", vc)
	}
	plain := ansi.Strip(out)
	if strings.Contains(plain, "```") || !strings.Contains(plain, "# comment") {
		t.Errorf("fences hidden, code verbatim:\n%s", plain)
	}
}

// cellsAt returns the w display cells of s starting at cell column x.
func cellsAt(s string, x, w int) string {
	col, out := 0, ""
	for _, r := range s {
		rw := runewidth.RuneWidth(r)
		if col >= x && col+rw <= x+w {
			out += string(r)
		}
		col += rw
	}
	return out
}
