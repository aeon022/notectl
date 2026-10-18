package tui

import (
	"strings"
	"testing"

	"github.com/aeon022/missionctl-core/theme"
	"github.com/aeon022/missionctl-core/tuitest"
)

func TestThemeKeySwitchesLiveAndSaves(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	orig := theme.Current()
	t.Cleanup(func() { _ = theme.Apply(orig) })

	m := newNotes(t, sampleNotes()...)
	m.loading = false
	before := styleHeader.GetForeground()
	mm, _ := tuitest.Send(m, tuitest.Key("T"))

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
