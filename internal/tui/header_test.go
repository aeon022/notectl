package tui

import (
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/aeon022/missionctl-core/tuitest"
	"github.com/aeon022/notectl/internal/models"
	"github.com/charmbracelet/x/ansi"
)

// chromeModel is a list model with two notebooks, the second ("Change-
// Management", active) with sub-folders — expanded when withFolders is set.
func chromeModel(t *testing.T, w, h int, withFolders bool) Model {
	t.Helper()
	m := newNotes(t)
	m.loading = false
	m.accounts = []string{"Die Brücke", "iCloud"}
	m.topFolders = []string{"Baby", "Change-Management", "Linux"}
	m.subFolders = map[string][]string{"Change-Management": {"Change-Management/Howtos", "Change-Management/KI"}}
	m.folderCounts = map[string]int{"": 90, "Baby": 5, "Change-Management": 23, "Change-Management/Howtos": 3, "Change-Management/KI": 2, "Linux": 1}
	m.lastSynced = time.Now().Add(-10 * time.Minute)
	now := time.Now()
	for i, title := range []string{"Modul 4-2 Prüfungs-Kompendium", "Baby Names", "Lern- und Abgabeplan MBA", "SERVER", "Mail Signatures"} {
		m.allNotes = append(m.allNotes, models.Note{ID: title, Title: title, Folder: "Change-Management/Howtos", Body: "body of " + title, ModTime: now.Add(-time.Duration(i) * 30 * time.Hour)})
	}
	m.notes = m.allNotes
	if withFolders {
		m.setExpanded(1, true)
	}
	m.tabCursor = m.cursorFor(2, -1) // "Change-Management"
	mi, _ := tuitest.Send(m, tuitest.Resize(w, h))
	return mi.(Model)
}

func TestChrome_TiersAndRows(t *testing.T) {
	for _, c := range []struct {
		h       int
		folders bool
		rows    int
		tabsY   int
		foldY   int
	}{
		{20, false, 3, 1, -1}, {20, true, 4, 1, 2}, // compact: header, tabs, [folders], divider
		{30, false, 5, 3, -1}, {40, true, 6, 3, 4}, // spacious: header, divider, blank, tabs, [folders], blank
	} {
		ch := chromeModel(t, 110, c.h, c.folders).chrome()
		if ch.rows != c.rows || ch.tabsY != c.tabsY || ch.foldersY != c.foldY {
			t.Errorf("height %d folders=%v: chrome = %+v, want rows %d tabs y=%d folders y=%d", c.h, c.folders, ch, c.rows, c.tabsY, c.foldY)
		}
	}
}

func TestRenderList_EveryLineFitsAndHeightIsExact(t *testing.T) {
	for _, size := range [][2]int{{60, 20}, {80, 24}, {100, 30}, {140, 40}, {170, 45}} {
		for _, folders := range []bool{false, true} {
			m := chromeModel(t, size[0], size[1], folders)
			out := m.renderList()
			lines := strings.Split(out, "\n")
			if len(lines) != m.height {
				t.Errorf("%dx%d folders=%v: %d lines, want exactly m.height=%d", size[0], size[1], folders, len(lines), m.height)
			}
			for i, l := range lines {
				if w := lipgloss.Width(l); w > size[0] {
					t.Errorf("%dx%d folders=%v: line %d is %d wide: %q", size[0], size[1], folders, i, w, ansi.Strip(l))
				}
			}
		}
	}
}

func TestFoldersRowOnlyWhenTheNotebookHasExpandedSubfolders(t *testing.T) {
	without := ansi.Strip(chromeModel(t, 110, 40, false).renderChrome())
	if strings.Contains(without, "Folders") || strings.Contains(without, "Howtos") {
		t.Errorf("no Folders row without an expanded notebook:\n%s", without)
	}
	with := ansi.Strip(chromeModel(t, 110, 40, true).renderChrome())
	for _, want := range []string{"Notebooks", "Folders", "Change-Management ›", "Howtos 3", "KI 2"} {
		if !strings.Contains(with, want) {
			t.Errorf("spacious chrome missing %q:\n%s", want, with)
		}
	}
	compact := ansi.Strip(chromeModel(t, 110, 20, true).renderChrome())
	if strings.Contains(compact, "Notebooks") || strings.Contains(compact, "Folders") || !strings.Contains(compact, "Howtos 3") {
		t.Errorf("compact tier drops the row labels (and the blank lines) but keeps the chips:\n%s", compact)
	}
	if n := strings.Count(compact, "\n") + 1; n != 4 {
		t.Errorf("compact chrome with folders = %d lines, want 4", n)
	}
}

func TestHeaderRowsAreSeparatedBySpacingInTheSpaciousTier(t *testing.T) {
	lines := strings.Split(ansi.Strip(chromeModel(t, 110, 40, true).renderChrome()), "\n")
	if len(lines) != 6 || strings.TrimSpace(lines[2]) != "" || strings.TrimSpace(lines[5]) != "" {
		t.Fatalf("want header, divider, blank, Notebooks, Folders, blank — got %q", lines)
	}
	if !strings.Contains(lines[0], "notectl") || !strings.Contains(lines[1], "─") || !strings.HasPrefix(strings.TrimSpace(lines[3]), "Notebooks") || !strings.HasPrefix(strings.TrimSpace(lines[4]), "Folders") {
		t.Errorf("unexpected chrome: %q", lines)
	}
	// the chips of both rows start in the same column
	if strings.Index(lines[3], "All") != strings.Index(lines[4], "▾") {
		t.Errorf("Notebooks and Folders content must align: %q / %q", lines[3], lines[4])
	}
}

func TestClicks_MapToTheRowsThatAreDrawn(t *testing.T) {
	for _, size := range [][2]int{{110, 20}, {110, 40}, {140, 40}, {170, 45}} {
		for _, folders := range []bool{false, true} {
			m := chromeModel(t, size[0], size[1], folders)
			ch := m.chrome()
			lines := strings.Split(ansi.Strip(m.renderList()), "\n")

			// Notebooks row: every visible tab resolves to itself, and the drawn line holds its label
			_, hits := m.tabBar(m.width - 1)
			for _, h := range hits {
				if row, idx := m.tabHitTest(1+h.x+h.w/2, ch.tabsY); row != 0 || idx != h.idx {
					t.Errorf("%v folders=%v: tab %d click -> (%d,%d)", size, folders, h.idx, row, idx)
				}
			}
			if !strings.Contains(lines[ch.tabsY], "Change-Management") {
				t.Errorf("%v: screen row %d should be the tabs row: %q", size, ch.tabsY, lines[ch.tabsY])
			}
			// Folders row
			if folders {
				_, fh := m.folderBar(m.width - 1)
				if len(fh) != 2 {
					t.Fatalf("%v: want 2 folder chips, got %v", size, fh)
				}
				for _, h := range fh {
					if row, idx := m.tabHitTest(1+h.x+h.w/2, ch.foldersY); row != 1 || idx != h.idx {
						t.Errorf("%v: folder chip %d click -> (%d,%d)", size, h.idx, row, idx)
					}
				}
				if !strings.Contains(lines[ch.foldersY], "Howtos") {
					t.Errorf("%v: screen row %d should be the folders row: %q", size, ch.foldersY, lines[ch.foldersY])
				}
			} else if row, _ := m.tabHitTest(5, 4); row == 1 {
				t.Errorf("%v: no folders row, y=4 must not hit a chip", size)
			}
			// first list row: the first note is the selected one (cursor 0), so its line carries the
			// accent bar — a click there must select note 0 (the right pane's preview text also
			// mentions the title, so don't search by title)
			noteY := -1
			for i := m.listStartY(); i < len(lines); i++ {
				if strings.Contains(lines[i], "▌") {
					noteY = i
					break
				}
			}
			if noteY < m.listStartY() || noteY > m.listStartY()+1 { // at most one date-group header above it
				t.Errorf("%v folders=%v: list starts at y=%d but the selected note is on line %d", size, folders, m.listStartY(), noteY)
				continue
			}
			if got := m.rowHitTest(5, noteY); got != 0 {
				t.Errorf("%v folders=%v: click on the first note's line %d -> note %d", size, folders, noteY, got)
			}
		}
	}
}

func TestSelectedListRowIsOneContinuousBar(t *testing.T) {
	m := chromeModel(t, 100, 30, false) // two-pane: " " + row + " │ " + preview
	var sel string
	for _, l := range strings.Split(m.renderList(), "\n") {
		if strings.Contains(ansi.Strip(l), "▌") {
			sel = l
		}
	}
	if sel == "" {
		t.Fatal("no selected row found")
	}
	// the row runs from its accent bar (and the SGR that colors it) to the pane divider
	accent := strings.Index(sel, "▌")
	from := strings.LastIndex(sel[:accent], "\x1b[")
	to := strings.Index(sel, "│")
	to = strings.LastIndex(sel[:to], "\x1b[")
	seg := sel[from:to]
	covered, total := bgCoverage(seg)
	// total includes the one separator cell between the row and the pane divider
	if total < 30 || covered < total-1 {
		t.Errorf("selected row: %d of %d cells after the accent bar have a background, want one continuous bar", covered, total)
	}
}

func TestFooterCarriesTheSyncAgeAmberWhenStale(t *testing.T) {
	m := chromeModel(t, 140, 40, false)
	fresh := m.renderHelpBar(140)
	if !strings.Contains(ansi.Strip(fresh), "synced 10m ago") || !strings.Contains(ansi.Strip(fresh), "1/5") {
		t.Errorf("footer: %q", ansi.Strip(fresh))
	}
	amber := strings.SplitN(styleSyncing.Render("x"), "x", 2)[0]
	if strings.Contains(fresh, amber) {
		t.Error("a fresh sync age must not be amber")
	}
	m.lastSynced = time.Now().Add(-30 * time.Hour)
	stale := m.renderHelpBar(140)
	if !strings.Contains(ansi.Strip(stale), "synced 1d ago") || !strings.Contains(stale, amber) {
		t.Errorf("a sync older than a day is amber: %q", ansi.Strip(stale))
	}
	if lipgloss.Width(stale) > 140 || strings.Contains(stale, "\n") {
		t.Error("footer stays one line within the width")
	}
}

func TestShortFolderKeepsTheEndOfThePath(t *testing.T) {
	for _, c := range []struct {
		in     string
		budget int
		want   string
	}{
		{"Notes", 20, "Notes"},
		{"Change-Management/Howtos/KI", 40, "Change-Management/Howtos/KI"},
		{"Change-Management/Howtos/KI", 14, "…/Howtos/KI"},
		{"Change-Management/Howtos/KI", 6, "…/KI"},
	} {
		if got := shortFolder(c.in, c.budget); got != c.want {
			t.Errorf("shortFolder(%q, %d) = %q, want %q", c.in, c.budget, got, c.want)
		}
	}
	if got := shortFolder("Ein/sehr-langer-letzter-Ordnername", 10); lipgloss.Width(got) > 10 || got == "" {
		t.Errorf("a too-long last segment is mid-ellipsized to fit: %q", got)
	}
}
