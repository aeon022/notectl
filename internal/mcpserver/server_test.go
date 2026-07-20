package mcpserver

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aeon022/notectl/internal/models"
	"github.com/aeon022/notectl/internal/store"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/spf13/viper"
)

// setupTest points config.DBPath()/config.VaultPath() at a temporary DB and a
// temporary directory via the existing viper overrides, and seeds the DB with
// one note. All handlers exercised here go through internal/notes/obsidian.go
// (plain filesystem I/O against the temp vault) — none of them touch
// internal/notes/apple.go's AppleScript integration.
func setupTest(t *testing.T) string {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "notectl.db")
	vaultPath := t.TempDir()
	viper.Set("db_path", dbPath)
	viper.Set("vault_path", vaultPath)
	t.Cleanup(func() {
		viper.Set("db_path", "")
		viper.Set("vault_path", "")
	})

	s, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	n := &models.Note{
		ID: "1", Title: "Meeting Notes", Body: "Discussed the roadmap.",
		Tags: []string{"work"}, Source: "obsidian", Path: "Meeting Notes.md",
	}
	if err := s.Upsert(context.Background(), n); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	return vaultPath
}

func callTool(t *testing.T, handler func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error), args map[string]any) *mcp.CallToolResult {
	t.Helper()
	req := mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: args}}
	res, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("handler returned error: %v", err)
	}
	if res.IsError {
		t.Fatalf("handler returned an error result: %+v", res.Content)
	}
	return res
}

func resultText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	if len(res.Content) == 0 {
		t.Fatal("result has no content")
	}
	tc, ok := res.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", res.Content[0])
	}
	return tc.Text
}

func TestToolsAreRegisteredWithValidSchema(t *testing.T) {
	for _, tc := range []struct {
		name string
		tool mcp.Tool
	}{
		{"list_notes", toolList()},
		{"read_note", toolRead()},
		{"write_note", toolWrite()},
		{"search_notes", toolSearch()},
		{"sync_notes", toolSync()},
		{"get_daily_note", toolGetDailyNote()},
		{"append_daily_note", toolAppendDailyNote()},
	} {
		if tc.tool.Name != tc.name {
			t.Errorf("expected tool name %q, got %q", tc.name, tc.tool.Name)
		}
		if tc.tool.Description == "" {
			t.Errorf("tool %q has no description", tc.name)
		}
	}
}

func TestHandleList(t *testing.T) {
	setupTest(t)

	res := callTool(t, handleList, nil)
	text := resultText(t, res)
	if !strings.Contains(text, "Meeting Notes") {
		t.Errorf("expected seeded note in output, got:\n%s", text)
	}
}

func TestHandleReadFromCache(t *testing.T) {
	setupTest(t)

	res := callTool(t, handleRead, map[string]any{"title": "Meeting Notes"})
	text := resultText(t, res)
	if !strings.Contains(text, "Discussed the roadmap.") {
		t.Errorf("expected note body in output, got:\n%s", text)
	}
}

func TestHandleWriteThenReadFromVault(t *testing.T) {
	setupTest(t)

	callTool(t, handleWrite, map[string]any{
		"title": "New Idea",
		"body":  "Build a widget.",
		"tags":  "ideas",
	})

	res := callTool(t, handleRead, map[string]any{"title": "New Idea"})
	text := resultText(t, res)
	if !strings.Contains(text, "Build a widget.") {
		t.Errorf("expected written note to be readable, got:\n%s", text)
	}
}

func TestHandleSearch(t *testing.T) {
	setupTest(t)

	res := callTool(t, handleSearch, map[string]any{"query": "roadmap"})
	text := resultText(t, res)
	if !strings.Contains(text, "Meeting Notes") {
		t.Errorf("expected search match in output, got:\n%s", text)
	}
}

func TestHandleSync(t *testing.T) {
	vaultPath := setupTest(t)
	_ = vaultPath

	// Write a note directly via handleWrite so it lands in the vault as a real
	// file, then sync should pick it up by re-scanning the vault. Single-word
	// title to avoid the (separate, pre-existing) title/filename slugify
	// round-trip turning spaces into dashes.
	callTool(t, handleWrite, map[string]any{"title": "SyncedNote", "body": "hello"})

	res := callTool(t, handleSync, nil)
	text := resultText(t, res)
	if !strings.Contains(text, "Synced") {
		t.Errorf("expected sync confirmation, got:\n%s", text)
	}

	listRes := callTool(t, handleList, nil)
	if !strings.Contains(resultText(t, listRes), "SyncedNote") {
		t.Error("expected synced note to appear in list_notes")
	}
}

func TestHandleGetDailyNoteCreatesFromTemplate(t *testing.T) {
	setupTest(t)

	res := callTool(t, handleGetDailyNote, nil)
	text := resultText(t, res)
	if !strings.Contains(text, "## Focus") {
		t.Errorf("expected daily note template in output, got:\n%s", text)
	}
}

func TestHandleAppendDailyNote(t *testing.T) {
	setupTest(t)

	callTool(t, handleGetDailyNote, nil)
	callTool(t, handleAppendDailyNote, map[string]any{
		"content": "Shipped the smoke tests.",
		"section": "Log",
	})

	res := callTool(t, handleGetDailyNote, nil)
	text := resultText(t, res)
	if !strings.Contains(text, "Shipped the smoke tests.") {
		t.Errorf("expected appended content in daily note, got:\n%s", text)
	}
}
