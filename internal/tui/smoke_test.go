package tui

import (
	"bytes"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
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
