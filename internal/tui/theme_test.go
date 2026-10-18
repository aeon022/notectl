package tui

import (
	"fmt"
	"github.com/aeon022/notectl/internal/models"
	"github.com/charmbracelet/x/ansi"
	"strings"
	"testing"

	"github.com/aeon022/missionctl-core/theme"
	"github.com/aeon022/missionctl-core/tuitest"
)

func TestThemeKeyOpensPickerWithLivePreview(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	orig := theme.Current()
	t.Cleanup(func() { _ = theme.Apply(orig) })

	m := newNotes(t, sampleNotes()...)
	m.loading = false
	before := styleHeader.GetForeground()
	mm, _ := tuitest.Send(m, tuitest.Key("T"))
	if !strings.Contains(tuitest.Text(mm), "Colour theme") {
		t.Fatalf("T opens the theme popup:\n%s", tuitest.Text(mm))
	}
	mm, _ = tuitest.Send(mm, tuitest.Key("j"))     // previews the next theme live
	mm, _ = tuitest.Send(mm, tuitest.Key("enter")) // keeps it

	if theme.Current() == orig {
		t.Fatalf("T must switch to the next theme, still %q", orig)
	}
	if styleHeader.GetForeground() == before {
		t.Error("cached styles must be rebuilt when the theme changes")
	}
	if got := mm.(Model).status; got != "Theme: "+theme.Current() {
		t.Errorf("a short confirmation should name the theme, got %q", got)
	}
}

func TestSettingsThemeKeyCyclesAndShowsTheme(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	orig := theme.Current()
	t.Cleanup(func() { _ = theme.Apply(orig) })

	m := newNotes(t, sampleNotes()...)
	m.loading = false
	m.view = viewSettings
	mm, _ := tuitest.Send(m, tuitest.Key("ctrl+t"))
	if theme.Current() == orig {
		t.Fatalf("ctrl+t in settings must switch the theme, still %q", orig)
	}
	if !strings.Contains(tuitest.Text(mm), theme.Current()) {
		t.Errorf("the settings screen should name the active theme:\n%s", tuitest.Text(mm))
	}
}

func TestLinkGraphScrollsWithTheCursor(t *testing.T) {
	m := newNotes(t, sampleNotes()...)
	m.loading = false
	m.width, m.height = 80, 14
	m.view = viewGraph
	focus := models.Note{ID: "f", Title: "Focus"}
	m.graphFocus = &focus
	for i := 0; i < 30; i++ {
		m.graphOut = append(m.graphOut, models.Note{ID: fmt.Sprintf("o%d", i), Title: fmt.Sprintf("Linked note %02d", i)})
	}
	if out := ansi.Strip(m.renderGraph()); strings.Contains(out, "Linked note 29") || !strings.Contains(out, "Linked note 00") {
		t.Fatalf("at the top the first neighbours show, the last do not:\n%s", out)
	}
	m.graphCursor = 29
	out := ansi.Strip(m.renderGraph())
	if !strings.Contains(out, "› ──▶ Linked note 29") {
		t.Errorf("the selected neighbour must stay visible while scrolling:\n%s", out)
	}
	if n := strings.Count(out, "\n") + 1; n > m.height {
		t.Errorf("the frame must keep its height: %d lines for height %d", n, m.height)
	}
}
