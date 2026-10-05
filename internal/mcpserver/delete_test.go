package mcpserver

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aeon022/notectl/internal/config"
	"github.com/aeon022/notectl/internal/models"
	"github.com/aeon022/notectl/internal/store"
	"github.com/mark3labs/mcp-go/mcp"
)

func seedDup(t *testing.T, vault, id, folder string) {
	t.Helper()
	rel := filepath.Join(folder, "Dup.md")
	if err := os.MkdirAll(filepath.Join(vault, folder), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vault, rel), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := store.New(config.DBPath(), false)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Upsert(context.Background(), &models.Note{ID: id, Title: "Dup", Folder: folder, Source: "obsidian", Path: rel}); err != nil {
		t.Fatal(err)
	}
}

// Irreversible: with duplicate titles it must refuse to guess.
func TestDeleteAmbiguousTitleDeletesNothing(t *testing.T) {
	vault := setupTest(t)
	seedDup(t, vault, "d1", "A")
	seedDup(t, vault, "d2", "B")

	text := resultText(t, callTool(t, handleDelete, map[string]any{"title": "Dup"}))
	if !strings.Contains(text, "nothing deleted") || !strings.Contains(text, `folder="A"`) || !strings.Contains(text, `folder="B"`) {
		t.Errorf("ambiguity message = %q", text)
	}
	for _, f := range []string{"A/Dup.md", "B/Dup.md"} {
		if _, err := os.Stat(filepath.Join(vault, f)); err != nil {
			t.Errorf("%s must still exist: %v", f, err)
		}
	}

	text = resultText(t, callTool(t, handleDelete, map[string]any{"title": "Dup", "folder": "B"}))
	if !strings.HasPrefix(text, "Deleted: Dup") {
		t.Errorf("folder-scoped delete = %q", text)
	}
	if _, err := os.Stat(filepath.Join(vault, "B/Dup.md")); !os.IsNotExist(err) {
		t.Error("B/Dup.md must be gone from disk")
	}
	if _, err := os.Stat(filepath.Join(vault, "A/Dup.md")); err != nil {
		t.Error("A/Dup.md must survive")
	}
	s, _ := store.New(config.DBPath(), false)
	defer s.Close()
	if left, _ := s.FindByTitle(context.Background(), "Dup"); len(left) != 1 || left[0].ID != "d1" {
		t.Errorf("cache after delete = %+v", left)
	}
}

func TestDeleteNotFoundAndMissingTitle(t *testing.T) {
	setupTest(t)
	if text := resultText(t, callTool(t, handleDelete, map[string]any{"title": "Ghost"})); !strings.Contains(text, "No note titled") {
		t.Errorf("not found = %q", text)
	}
	req := callToolRaw(t, handleDelete, map[string]any{})
	if !req.IsError {
		t.Error("missing title must be an error result")
	}
}

func callToolRaw(t *testing.T, h func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error), args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := h(context.Background(), mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: args}})
	if err != nil {
		t.Fatal(err)
	}
	return res
}
