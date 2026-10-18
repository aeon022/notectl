package tui

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/aeon022/missionctl-core/tuitest"
	"github.com/aeon022/notectl/internal/models"
)

// TestRenderLandingMockups writes an ANSI screenshot of the real TUI (made-up demo data) for the landing
// page: NOTECTL_MOCKUP_DIR=/dir go test ./internal/tui -run TestRenderLandingMockups
func TestRenderLandingMockups(t *testing.T) {
	dir := os.Getenv("NOTECTL_MOCKUP_DIR")
	if dir == "" {
		t.Skip("set NOTECTL_MOCKUP_DIR to render landing mockups")
	}
	now := time.Now()
	ago := func(h int) time.Time { return now.Add(-time.Duration(h) * time.Hour) }
	note := func(id, title, folder, body string, h int, tags ...string) models.Note {
		return models.Note{ID: id, Title: title, Folder: folder, Body: body, Tags: tags, ModTime: ago(h), Created: ago(h + 48), Source: "obsidian"}
	}
	ns := []models.Note{
		note("1", "Roadmap Q4", "Projects", "# Roadmap Q4\n\n- Ship [[Landing page]]\n- Theme switcher in every tool\n\nSee [[Meeting notes]].", 1, "work", "plan"),
		note("2", "Landing page", "Projects", "Hero scene, theme tabs, docs.", 3, "work"),
		note("3", "Meeting notes", "Work", "Standup + planning.", 5, "work", "meeting"),
		note("4", "Reading list", "Personal", "Books to read this winter.", 20, "home"),
		note("5", "Weekend hike", "Personal", "Routes and gear.", 30, "home", "outdoor"),
		note("6", "Daily 2026-10-17", "Daily", "- shipped security fixes", 26, "daily"),
		note("7", "Ideas", "Inbox", "Random thoughts.", 50),
	}
	m := New("")
	t.Setenv("HOME", t.TempDir())
	mi, _ := tuitest.Send(m, tea.WindowSizeMsg{Width: 118, Height: 20}, notesLoadedMsg{notes: ns, folders: []string{"Projects", "Work", "Personal", "Daily", "Inbox"}, folderCounts: map[string]int{"Projects": 2, "Work": 1, "Personal": 2, "Daily": 1, "Inbox": 1}})
	out := mi.(Model).View().Content
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notectl.ansi"), []byte(out), 0o644); err != nil {
		t.Fatal(err)
	}
}
