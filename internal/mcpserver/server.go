package mcpserver

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/aeon022/notectl/internal/config"
	"github.com/aeon022/notectl/internal/notes"
	"github.com/aeon022/notectl/internal/store"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func Serve() error {
	s := server.NewMCPServer("notectl", "0.1.0",
		server.WithToolCapabilities(true),
	)
	s.AddTool(toolList(), handleList)
	s.AddTool(toolRead(), handleRead)
	s.AddTool(toolWrite(), handleWrite)
	s.AddTool(toolSearch(), handleSearch)
	s.AddTool(toolSync(), handleSync)
	s.AddTool(toolGetDailyNote(), handleGetDailyNote)
	s.AddTool(toolAppendDailyNote(), handleAppendDailyNote)
	return server.ServeStdio(s)
}

func toolList() mcp.Tool {
	return mcp.NewTool("list_notes",
		mcp.WithDescription("List notes from the local cache. Returns title, folder, tags, and modification date. Sorted by most recently modified."),
		mcp.WithString("folder", mcp.Description("Filter by folder/subdirectory")),
		mcp.WithString("source", mcp.Description("Filter by source: obsidian or apple")),
		mcp.WithNumber("limit", mcp.Description("Max results (default 50)")),
	)
}

func toolRead() mcp.Tool {
	return mcp.NewTool("read_note",
		mcp.WithDescription("Read the full content of a note by title. Returns title, tags, folder, and body."),
		mcp.WithString("title", mcp.Required(), mcp.Description("Note title (exact or approximate)")),
	)
}

func toolWrite() mcp.Tool {
	return mcp.NewTool("write_note",
		mcp.WithDescription("Write or update a note in the Obsidian vault. Creates the file if it doesn't exist."),
		mcp.WithString("title", mcp.Required(), mcp.Description("Note title (becomes the filename)")),
		mcp.WithString("body", mcp.Required(), mcp.Description("Note content in Markdown")),
		mcp.WithString("folder", mcp.Description("Subfolder within vault (optional)")),
		mcp.WithString("tags", mcp.Description("Comma-separated tags")),
	)
}

func toolSearch() mcp.Tool {
	return mcp.NewTool("search_notes",
		mcp.WithDescription("Search notes by keyword across title and content. Returns matching notes with preview."),
		mcp.WithString("query", mcp.Required(), mcp.Description("Search term")),
		mcp.WithNumber("limit", mcp.Description("Max results (default 10)")),
	)
}

func toolSync() mcp.Tool {
	return mcp.NewTool("sync_notes",
		mcp.WithDescription("Sync the Obsidian vault into the local cache. Call this if note list seems stale."),
	)
}

// ── Handlers ──────────────────────────────────────────────────────────────────

func handleList(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	folder := req.GetString("folder", "")
	source := req.GetString("source", "")
	limit := int(req.GetFloat("limit", 50))

	s, err := store.New(config.DBPath())
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer s.Close()

	ns, err := s.List(context.Background(), store.Filter{
		Folder: folder,
		Source: source,
		Limit:  limit,
	})
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if len(ns) == 0 {
		return mcp.NewToolResultText("No notes found. Run sync_notes first."), nil
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("%d notes:\n\n", len(ns)))
	for _, n := range ns {
		b.WriteString(fmt.Sprintf("• %s", n.Title))
		if n.Folder != "" {
			b.WriteString(fmt.Sprintf(" (%s)", n.Folder))
		}
		if len(n.Tags) > 0 {
			b.WriteString(fmt.Sprintf(" [%s]", strings.Join(n.Tags, ", ")))
		}
		b.WriteString(fmt.Sprintf("  — %s\n", n.ModTime.Format("02 Jan 2006")))
	}
	return mcp.NewToolResultText(b.String()), nil
}

func handleRead(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	title := req.GetString("title", "")
	if title == "" {
		return mcp.NewToolResultError("title is required"), nil
	}

	s, err := store.New(config.DBPath())
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer s.Close()

	n, err := s.GetByTitle(context.Background(), title)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if n == nil {
		// live read from vault
		n, err = notes.Read(config.VaultPath(), title)
		if err != nil || n == nil {
			return mcp.NewToolResultError(fmt.Sprintf("note %q not found", title)), nil
		}
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("# %s\n", n.Title))
	if n.Folder != "" {
		b.WriteString(fmt.Sprintf("Folder: %s\n", n.Folder))
	}
	if len(n.Tags) > 0 {
		b.WriteString(fmt.Sprintf("Tags: %s\n", strings.Join(n.Tags, ", ")))
	}
	b.WriteString(fmt.Sprintf("Modified: %s\n\n", n.ModTime.Format("02 Jan 2006 15:04")))
	b.WriteString(n.Body)
	return mcp.NewToolResultText(b.String()), nil
}

func handleWrite(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	title := req.GetString("title", "")
	body := req.GetString("body", "")
	folder := req.GetString("folder", "")
	tagsStr := req.GetString("tags", "")
	if title == "" || body == "" {
		return mcp.NewToolResultError("title and body are required"), nil
	}

	var tags []string
	for _, t := range strings.Split(tagsStr, ",") {
		if t = strings.TrimSpace(t); t != "" {
			tags = append(tags, t)
		}
	}

	n, err := notes.Write(config.VaultPath(), title, body, tags, folder)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	// update cache
	if s, serr := store.New(config.DBPath()); serr == nil {
		defer s.Close()
		_ = s.Upsert(context.Background(), n)
	}

	return mcp.NewToolResultText(fmt.Sprintf("Wrote: %s → %s", n.Title, n.Path)), nil
}

func handleSearch(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	query := req.GetString("query", "")
	limit := int(req.GetFloat("limit", 10))
	if query == "" {
		return mcp.NewToolResultError("query is required"), nil
	}

	s, err := store.New(config.DBPath())
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer s.Close()

	results, err := s.List(context.Background(), store.Filter{Query: query, Limit: limit})
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if len(results) == 0 {
		results, _ = notes.Search(config.VaultPath(), query, limit)
	}
	if len(results) == 0 {
		return mcp.NewToolResultText(fmt.Sprintf("No notes found for %q", query)), nil
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("Found %d notes for %q:\n\n", len(results), query))
	for _, n := range results {
		b.WriteString(fmt.Sprintf("## %s\n", n.Title))
		if n.Folder != "" {
			b.WriteString(fmt.Sprintf("Folder: %s  ", n.Folder))
		}
		b.WriteString(fmt.Sprintf("Modified: %s\n", n.ModTime.Format("02 Jan 2006")))
		preview := strings.ReplaceAll(n.Body, "\n", " ")
		if len(preview) > 300 {
			preview = preview[:298] + "…"
		}
		if preview != "" {
			b.WriteString(preview + "\n")
		}
		b.WriteString("\n")
	}
	return mcp.NewToolResultText(b.String()), nil
}

func toolGetDailyNote() mcp.Tool {
	return mcp.NewTool("get_daily_note",
		mcp.WithDescription("Get today's daily note. Creates one from the standard template if it doesn't exist yet. Returns full Markdown content."),
		mcp.WithString("folder", mcp.Description("Subfolder for daily notes (default: Daily)")),
	)
}

func toolAppendDailyNote() mcp.Tool {
	return mcp.NewTool("append_daily_note",
		mcp.WithDescription("Append content to today's daily note. Use this to log insights, tasks, or observations without overwriting existing content."),
		mcp.WithString("content", mcp.Required(), mcp.Description("Markdown content to append")),
		mcp.WithString("section", mcp.Description("Section heading to append under (e.g. 'Log', 'Tasks'). Appends at end if not found.")),
		mcp.WithString("folder", mcp.Description("Subfolder for daily notes (default: Daily)")),
	)
}

func handleGetDailyNote(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	folder := req.GetString("folder", "Daily")
	today := time.Now().Format("2006-01-02")
	vaultPath := config.VaultPath()

	n, _ := notes.Read(vaultPath, today)
	if n != nil {
		return mcp.NewToolResultText(fmt.Sprintf("# %s\n\n%s", n.Title, n.Body)), nil
	}

	// create from template
	body := dailyNoteTemplate()
	n, err := notes.Write(vaultPath, today, body, []string{"daily"}, folder)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("create daily note: %v", err)), nil
	}
	if s, serr := store.New(config.DBPath()); serr == nil {
		defer s.Close()
		_ = s.Upsert(context.Background(), n)
	}
	return mcp.NewToolResultText(fmt.Sprintf("# %s\n\n%s", n.Title, n.Body)), nil
}

func handleAppendDailyNote(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	content := req.GetString("content", "")
	section := req.GetString("section", "")
	folder := req.GetString("folder", "Daily")
	if content == "" {
		return mcp.NewToolResultError("content is required"), nil
	}

	today := time.Now().Format("2006-01-02")
	vaultPath := config.VaultPath()

	n, _ := notes.Read(vaultPath, today)
	if n == nil {
		// create first
		body := dailyNoteTemplate()
		var err error
		n, err = notes.Write(vaultPath, today, body, []string{"daily"}, folder)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("create daily note: %v", err)), nil
		}
	}

	newBody := appendToSection(n.Body, section, content)
	var tags []string
	if len(n.Tags) > 0 {
		tags = n.Tags
	} else {
		tags = []string{"daily"}
	}

	n, err := notes.Write(vaultPath, today, newBody, tags, folder)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if s, serr := store.New(config.DBPath()); serr == nil {
		defer s.Close()
		_ = s.Upsert(context.Background(), n)
	}
	return mcp.NewToolResultText(fmt.Sprintf("Appended to %s", today)), nil
}

func appendToSection(body, section, content string) string {
	if section == "" {
		return body + "\n" + content
	}
	// find the section heading and insert after it
	heading := "## " + section
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) == heading {
			// find next non-empty line or end of section
			insertAt := i + 1
			for insertAt < len(lines) && strings.TrimSpace(lines[insertAt]) == "" {
				insertAt++
			}
			result := make([]string, 0, len(lines)+2)
			result = append(result, lines[:insertAt]...)
			result = append(result, content)
			result = append(result, lines[insertAt:]...)
			return strings.Join(result, "\n")
		}
	}
	// section not found — append at end
	return body + "\n## " + section + "\n" + content
}

func dailyNoteTemplate() string {
	return `## Focus


## Tasks
- [ ]

## Notes


## Log

`
}

func handleSync(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ns, err := notes.List(config.VaultPath())
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	s, err := store.New(config.DBPath())
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer s.Close()
	ctx := context.Background()
	_ = s.DeleteBySource(ctx, "obsidian")
	for i := range ns {
		_ = s.Upsert(ctx, &ns[i])
	}
	return mcp.NewToolResultText(fmt.Sprintf("Synced %d notes from vault", len(ns))), nil
}
