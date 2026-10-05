package tui

import (
	"bytes"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/aeon022/missionctl-core/tuitest"
	"github.com/aeon022/notectl/internal/models"
	"github.com/charmbracelet/x/ansi"
)

// TestProgramSmoke drives the real tea.Program end to end (the same
// wiring Run() uses, including the FPS cap and motion-throttle filter) —
// startup, a resize, a few keypresses, a mouse click and motion burst,
// then quit — checking only that it never panics and produces a
// non-empty, correctly-headed final frame. This is diagnostic-only
// coverage for the v2 migration itself, not a replacement for a live
// human check of the actual rendered TUI.
func TestProgramSmoke(t *testing.T) {
	m := New("")
	pr, pw := io.Pipe()
	defer pw.Close()
	var out safeBuf

	p := tea.NewProgram(m, tea.WithInput(pr), tea.WithOutput(&out),
		tea.WithFilter(motionThrottleFilter()), tea.WithFPS(30))

	done := make(chan struct{})
	go func() {
		_, _ = p.Run()
		close(done)
	}()

	time.Sleep(50 * time.Millisecond)
	p.Send(tea.WindowSizeMsg{Width: 100, Height: 40})
	time.Sleep(20 * time.Millisecond)
	p.Send(tea.KeyPressMsg{Text: "j", Code: 'j'})
	p.Send(tea.KeyPressMsg{Text: "k", Code: 'k'})
	p.Send(tea.MouseClickMsg{Button: tea.MouseLeft, X: 5, Y: 5})
	for i := 0; i < 10; i++ {
		p.Send(tea.MouseMotionMsg{X: i, Y: 5})
	}
	time.Sleep(50 * time.Millisecond)
	p.Quit()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("program did not quit within 3s")
	}

	frame := out.String()
	if !strings.Contains(frame, "notectl") {
		t.Errorf("final frame missing header text, got:\n%s", frame)
	}
}

type safeBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *safeBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *safeBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// ── tuitest-based smoke runs: every view/mode, wide + narrow + empty ─────────

// visitEverything walks list → detail → link graph → help → tag browser →
// search → palette → new note → settings → notebook tabs/expand, leaving each
// with esc.
var visitEverything = []string{
	"j", "k", "enter", "L", "esc", "esc", // detail → link graph
	"?", "esc", // help popup
	"t", "j", "esc", // tag browser
	"/", "a", "esc", "esc", // search
	":", "esc", // palette
	"n", "esc", // new note
	"e", "esc", // edit
	"p", "esc", // settings
	"tab", "shift+tab", "right", "left", // accounts / notebooks
	"S", "H", // sort, hide empty
}

func TestSmokeAllViews(t *testing.T) {
	for _, tc := range []struct {
		name  string
		w, h  int
		notes []models.Note
	}{
		{"single-pane", 90, 30, sampleNotes()},
		{"two-pane", 140, 40, sampleNotes()},
		{"narrow", 60, 15, sampleNotes()},
		{"empty", 100, 30, nil},
		{"empty-narrow", 60, 15, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newNotes(t, tc.notes...)
			m.loading = false
			var tm tea.Model = m
			tm, _ = tuitest.Send(tm, tuitest.Resize(tc.w, tc.h))
			tuitest.Smoke(t, tm, visitEverything...)
		})
	}
}

func TestFootersNeverWrap(t *testing.T) {
	n := sampleNotes()[0]
	for _, w := range []int{60, 80, 100} {
		m := newNotes(t, sampleNotes()...)
		m.loading = false
		m.width, m.height = w, 30
		d := m
		d.detail = &n
		d.view = viewDetail
		for name, text := range map[string]string{
			"list":   ansi.Strip(m.renderList()),
			"detail": ansi.Strip(d.renderDetail()),
		} {
			for _, l := range strings.Split(text, "\n") {
				if lw := lipgloss.Width(l); lw > w {
					t.Errorf("%s at width %d: line is %d wide: %q", name, w, lw, l)
				}
			}
		}
	}
}

func TestEmptyAndLoadingStates(t *testing.T) {
	m := newNotes(t)
	m.loading = true
	if out := ansi.Strip(m.renderList()); !strings.Contains(out, "Loading notes…") {
		t.Errorf("loading state missing:\n%s", out)
	}
	m.loading = false
	out := ansi.Strip(m.renderList())
	if !strings.Contains(out, "No notes") || !strings.Contains(out, emptySuggestion()) {
		t.Errorf("empty state should show title and the source-specific suggestion:\n%s", out)
	}
	if emptySuggestion() == emptyHint() {
		t.Error("emptySuggestion must drop the \"No notes — \" lead")
	}
}
