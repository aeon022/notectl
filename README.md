# notectl

Terminal notes client for Obsidian vaults. Syncs markdown files to SQLite, provides a fast TUI and an MCP server for AI agents.

## Features

- Full-screen TUI with folder tabs, search, create/edit/delete notes
- Syncs any Obsidian vault (reads YAML frontmatter: tags, created date)
- Open notes in your default editor with `o` (Obsidian, Typora, etc.)
- MCP server: exposes list, read, write, search, sync tools to AI agents
- SQLite store with WAL mode — fast full-text search

## Requirements

- macOS (or Linux — no Apple-specific dependencies)
- Go 1.21+
- An Obsidian vault (or any folder of `.md` files)

## Setup

```bash
git clone https://github.com/aeon022/notectl
cd notectl
./setup.sh
```

Or manually:

```bash
go build -o ~/.local/bin/notectl .
notectl sync          # index your vault
notectl               # open TUI
```

## Configuration

Create `~/.config/notectl/notectl.yaml`:

```yaml
vault_path: ~/Documents/ObsidianVault   # path to your vault
```

Or set via environment variable:

```bash
export NOTECTL_VAULT_PATH=~/Documents/ObsidianVault
```

Default vault path: `~/Documents/Notes`

## TUI Keybindings

### List view
| Key | Action |
|-----|--------|
| `enter` | Open note |
| `n` | New note |
| `e` | Edit current note |
| `d` | Delete note |
| `o` | Open in default editor |
| `s` | Sync vault |
| `/` | Search (title, body, tags) |
| `tab` / `shift+tab` | Switch folder |
| `j` / `k` or `↓` / `↑` | Navigate |
| `PgDn` / `PgUp` | Page down / up |
| `g` / `G` | First / last note |
| `q` | Quit |

### Detail view
| Key | Action |
|-----|--------|
| `esc` | Back to list |
| `e` | Edit note |
| `d` | Delete note |
| `o` | Open in default editor |
| `↑` / `↓` | Scroll body |

### Edit view
| Key | Action |
|-----|--------|
| `tab` / `shift+tab` | Next / previous field |
| `ctrl+s` | Save |
| `esc` | Cancel |

## CLI Commands

```bash
notectl               # open TUI (default)
notectl tui           # open TUI
notectl sync          # sync vault to SQLite
notectl list          # list notes (JSON with --json)
notectl read TITLE    # read a note
notectl write TITLE   # write a note (reads body from stdin)
notectl search QUERY  # search notes
notectl mcp           # start MCP server (stdio)
```

## MCP Server

Add to your Claude Desktop config (`~/Library/Application Support/Claude/claude_desktop_config.json`):

```json
{
  "mcpServers": {
    "notectl": {
      "command": "notectl",
      "args": ["mcp"]
    }
  }
}
```

Available tools: `list_notes`, `read_note`, `write_note`, `search_notes`, `sync_notes`

## Note Format

Notes are standard Obsidian markdown files with optional YAML frontmatter:

```markdown
---
tags:
  - project
  - ideas
created: 2026-06-01
---

# My Note

Content here...
```

Notes created via the TUI or CLI follow this format automatically.

## Architecture

```
Obsidian Vault (.md files)
    ↓ sync
SQLite (~/.local/share/notectl/notes.db)
    ↓
TUI (Bubbletea)  ←→  MCP Server (stdio)
```
