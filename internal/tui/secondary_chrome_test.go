package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Every secondary view shares the chrome: "notectl · <View>" header, divider,
// exactly m.height lines, one-line footer with an esc hint, nothing too wide.
func TestSecondaryViewsShareChrome(t *testing.T) {
	views := map[string]func(m Model) string{
		"detail": func(m Model) string {
			n := sampleNotes()[0]
			m.detail, m.view = &n, viewDetail
			return m.renderDetail()
		},
		"new": func(m Model) string {
			m.resetNew("")
			m.view = viewNew
			return m.renderNew()
		},
		"settings": func(m Model) string { m.view = viewSettings; return m.renderSettings() },
		"graph": func(m Model) string {
			m = m.setGraphFocus(sampleNotes()[0])
			m.view = viewGraph
			return m.renderGraph()
		},
	}
	for name, render := range views {
		for _, w := range []int{40, 60, 80, 100, 140, 170} {
			for _, h := range []int{20, 40} {
				m := newNotes(t, sampleNotes()...)
				m.loading = false
				m.width, m.height = w, h
				m.bodyArea.SetWidth(m.editorBodyWidth())
				m.bodyArea.SetHeight(m.editorBodyHeight())
				lines := strings.Split(ansi.Strip(render(m)), "\n")
				if len(lines) != h {
					t.Errorf("%s %dx%d: %d lines, want %d", name, w, h, len(lines), h)
				}
				for _, l := range lines {
					if lipgloss.Width(l) > w {
						t.Errorf("%s %dx%d: line %d wide: %q", name, w, h, lipgloss.Width(l), l)
					}
				}
				if !strings.Contains(lines[0], "notectl") {
					t.Errorf("%s %dx%d: header missing: %q", name, w, h, lines[0])
				}
				if last := lines[len(lines)-1]; !strings.Contains(last, "esc") {
					t.Errorf("%s %dx%d: footer lacks esc hint: %q", name, w, h, last)
				}
			}
		}
	}
}

func TestPopupsFitAndHavePanelBorder(t *testing.T) {
	for _, w := range []int{40, 60, 100, 170} {
		m := newNotes(t, sampleNotes()...)
		m.loading = false
		m.width, m.height = w, 30
		for name, out := range map[string]string{"tags": m.renderTags(), "help": m.openHelp().renderHelpPopup()} {
			lines := strings.Split(ansi.Strip(out), "\n")
			for _, l := range lines {
				if lipgloss.Width(l) > w {
					t.Errorf("%s at %d: line %d wide", name, w, lipgloss.Width(l))
				}
			}
			if len(lines) > 30 || !strings.ContainsAny(lines[0], "╭┌┏") {
				t.Errorf("%s at %d: not a bordered panel (%d lines): %q", name, w, len(lines), lines[0])
			}
		}
	}
}
