# notectl

Terminal notes client — Obsidian vaults, Apple Notes, or Joplin. Part of the [missionctl](https://github.com/aeon022/missionctl) suite.

Syncs notes from your chosen source (or several at once) to a local SQLite cache, exposes a fast full-screen TUI, and runs an MCP server so AI agents can read and write your notes.

---

## Quick Start

1. **Clone and build**
   ```bash
   git clone https://github.com/aeon022/notectl && cd notectl
   ./setup.sh
   ```

2. **Configure your vault**
   ```bash
   # ~/.config/notectl/notectl.yaml
   vault_path: ~/Documents/ObsidianVault
   source: obsidian
   ```

3. **Index the vault, then confirm the config actually points where you think**
   ```bash
   notectl sync
   notectl doctor
   ```
   `doctor` flags `vault_path` if it's still the built-in default (`~/Documents/Notes`) — every note `write`/`write_note` creates for `source: obsidian`/`markdown` lands there, so it's worth confirming this is really your vault before you write anything through it.

4. **Open the TUI**
   ```bash
   notectl
   ```

5. **Wire up Claude Desktop** (optional — see [MCP section](#mcp--ai-integration))

---

## Cheatsheet

```
notectl                         open TUI (default)
notectl sync                    index vault into SQLite
notectl list [--folder NAME]    list notes
notectl read TITLE              print a note
notectl write TITLE             create/update a note
notectl search QUERY            full-text search
notectl daily                   open/create today's daily note
notectl mcp                     start MCP server (stdio)
```

**TUI one-liners**

```
j/k   navigate    /   search     n   new note    e   edit
s     sync        d   delete     o   open editor  q  quit
tab   next folder enter  open detail  esc  back
```

---

## CLI Reference

### `notectl` / `notectl tui`

Open the full-screen TUI. No flags required.

```bash
notectl
notectl tui
```

---

### `notectl sync`

Walk the vault and index all `.md` files into the SQLite cache. Run this after adding or editing notes outside the TUI.

```bash
notectl sync
```

---

### `notectl list`

List notes from the cache.

| Flag | Description |
|------|-------------|
| `--folder NAME` | Filter by folder / subfolder name |
| `--json` | Output as JSON array |

```bash
notectl list
notectl list --folder Projects
notectl list --folder Daily --json
```

---

### `notectl read TITLE`

Print the full content of a note by title (case-insensitive match against the cache).

| Flag | Description |
|------|-------------|
| `--json` | Output as JSON with metadata fields |

```bash
notectl read "Meeting notes"
notectl read "2026-07-04" --json
```

---

### `notectl write TITLE`

Create or overwrite a note. If `--body` is omitted, the body is read from stdin.

| Flag | Description |
|------|-------------|
| `--body TEXT` | Note body (plain text or markdown) |
| `--folder NAME` | Target subfolder inside the vault |
| `--tags tag1,tag2` | Comma-separated tags added to frontmatter |

```bash
# Inline body
notectl write "Sprint plan" --body "# Goals\n- Ship v1" --folder Projects --tags sprint,planning

# Body from stdin
echo "rough idea" | notectl write "Scratch" --folder Inbox

# Pipe from a file
cat meeting.md | notectl write "Q3 kickoff" --folder Meetings --tags quarterly
```

---

### `notectl search QUERY`

Full-text search across note titles and bodies.

| Flag | Description |
|------|-------------|
| `--json` | Output matches as JSON with previews |

```bash
notectl search "architecture decision"
notectl search obsidian --json
```

---

### `notectl daily`

Open or create today's daily note. Title format: `YYYY-MM-DD`. Applies the Focus / Tasks / Notes / Log template when creating a new note.

| Flag | Description |
|------|-------------|
| `--folder NAME` | Target folder (default: `Daily`) |
| `--open` | Open the note in `$EDITOR` after creating |

```bash
notectl daily
notectl daily --folder Journal
notectl daily --folder Daily --open
```

---

### `notectl mcp`

Start the MCP server on stdio. Intended to be launched by an MCP host (Claude Desktop, agent framework) rather than invoked directly.

```bash
notectl mcp
```

---

## TUI Keys

### List view

| Key | Action |
|-----|--------|
| `j` / `k` | Move down / up |
| `enter` | Open note detail |
| `n` | New note |
| `e` | Edit selected note |
| `d` | Delete note (confirm with `y`) |
| `o` | Open in `$EDITOR` |
| `s` | Sync vault |
| `/` | Search title + body |
| `tab` / `shift+tab` | Switch folder tab |
| `PgDn` / `PgUp` | Page down / up |
| `g` / `G` | First / last note |
| `q` | Quit |

### Detail and edit views

| Key | Action |
|-----|--------|
| `esc` | Back to list |
| `e` | Edit note |
| `d` | Delete note |
| `o` | Open in `$EDITOR` |
| `j` / `k` | Scroll body |

### Editor syntax

The new/edit view is a plain-text editor; on wide terminals (≥100 columns) a live
preview pane renders the markdown as you type. When syncing to Apple Notes the
syntax maps to native formatting:

| You type | TUI preview | Apple Notes |
|----------|-------------|-------------|
| `# ` / `## ` / `### ` | styled heading | Title / Heading / Subheading |
| `- item` / `* item` / `• item` | `•` bullet | bullet list |
| `- [ ] item` / `☐ item` | `☐` | checklist (unchecked) |
| `- [x] item` / `☑ item` | `☑` strikethrough | checklist (checked) |
| `**bold**` | bold | bold |
| `*italic*` | italic | italic |
| `~~strike~~` | strikethrough | strikethrough |
| `` `code` `` | highlighted | monospace |

Bold/italic/strikethrough/monospace round-trip: Apple Notes formatting comes
back as `**`/`*`/`~~`/`` ` `` markers when reading or editing the note in
notectl.

The editor supports the mouse: click a field or a position in the body to
move the cursor there; the wheel scrolls the body.

---

## Note Format

Notes are standard Obsidian-compatible markdown files with optional YAML frontmatter.

```markdown
---
tags:
  - project
created: 2026-07-04
---

# Note title

Body content here.
```

Notes created by the TUI or CLI always follow this structure. Existing vault notes without frontmatter are indexed as-is.

### Daily note template

`notectl daily` creates the following template when no note exists for today:

```markdown
# 2026-07-04

## Focus


## Tasks
- [ ]

## Notes


## Log
```

---

## Configuration

Config file: `~/.config/notectl/notectl.yaml`

```yaml
vault_path: ~/Documents/ObsidianVault
source: obsidian   # obsidian | apple | markdown | joplin
```

| Key | Description |
|-----|-------------|
| `vault_path` | Absolute or `~`-prefixed path to the vault root |
| `source` | Where notes are read/written: `obsidian` (default), `apple` (Apple Notes), `markdown` (plain folder), `joplin` (via Joplin's Data API — see below) |
| `sync_sources` | Comma-separated list, e.g. `apple,joplin` — sync several sources into one combined cache at once. Doesn't change where new notes are *written*; that's still whatever `source` is set to. Defaults to just `[source]` when unset. |
| `data_dir` | Directory for notectl's own SQLite index — set this to sync it across devices (see below) |
| `joplin_api_url` | Joplin's local Data API base URL. Default `http://localhost:41184`. |
| `joplin_token` | Joplin Data API auth token (Options → Web Clipper). Required for `source: joplin` or when `joplin` is in `sync_sources`. |
| `joplin_folder` | Optional notebook to scope Joplin sync/list to — a name, or `Parent/Child` for a nested one. Empty = every notebook. |

Environment variable override:

```bash
export NOTECTL_VAULT_PATH=~/Documents/ObsidianVault
```

### Syncing across devices

By default notectl's index lives at `~/.local/share/notectl/notes.db`, local to this machine. To share it across devices, set `data_dir` (or `NOTECTL_DATA_DIR`) to a folder you already sync yourself — iCloud Drive, Dropbox, Syncthing, etc:

```bash
export NOTECTL_DATA_DIR="$HOME/Library/Mobile Documents/com~apple~CloudDocs/notectl"
```

Once set, notectl automatically switches its SQLite journal mode from WAL to rollback-journal — WAL splits the database across multiple files that a folder-sync client can't update atomically together, so this switch keeps the directory down to a single consistent file whenever notectl isn't actively writing. A same-machine lock also prevents two notectl processes from opening the index at once (run `notectl doctor` to see the current mode and path). This only protects against the same-machine and stale-snapshot failure modes, not two machines editing at the exact same instant; an undownloaded iCloud file is reported explicitly rather than as a bare error.

### Apple Notes (`source: apple`) & Checklists

When `source: apple` is configured, `notectl` syncs directly with the native macOS **Notes.app**.

#### Full Disk Access Requirement for GUI Checklists
When reading notes via standard macOS AppleScript (`body of note`), macOS itself strips internal checklist metadata and returns all checklist items as plain HTML bullets (`<ul><li>item</li></ul>`), without distinguishing between checked (`☑`) and unchecked (`☐`) status.

To allow `notectl` to accurately synchronize the `checked` vs `unchecked` status of checklists created inside the native Apple Notes GUI:
1. Open **System Settings → Privacy & Security → Full Disk Access**.
2. Grant **Full Disk Access** to your terminal emulator (e.g., `Terminal`, `iTerm2`, `Alacritty`, or `Claude Desktop`).
3. Restart your terminal application.

When Full Disk Access is enabled, `notectl` can read `NoteStore.sqlite` (`~/Library/Group Containers/group.com.apple.notes/NoteStore.sqlite`) without macOS Sandboxing restrictions and preserve accurate checklist states across CLI, TUI, and AI MCP tools.

---

### Joplin (`source: joplin`)

Unlike Apple Notes (AppleScript) and Obsidian (plain files on disk), a standalone tool can't reach into a live Joplin process directly — so this talks to Joplin's **Data API** instead, the same local REST service browser clippers use.

1. In Joplin: **Options → Web Clipper → Enable Web Clipper Service**. Copy the token shown below it.
2. Set `source: joplin` and `joplin_token` in `~/.config/notectl/notectl.yaml` (or `NOTECTL_SOURCE`/`NOTECTL_JOPLIN_TOKEN`).
3. `notectl doctor` confirms it's reachable before you rely on it.

```yaml
source: joplin
joplin_token: <token from Joplin's Web Clipper settings>
joplin_folder: MISSIONCTL/Marketing   # optional — a notebook, or Parent/Child for a nested one
```

Joplin must be running with Web Clipper enabled for any Joplin operation to work — `notectl` fails with a clear message (not a silent no-op) if it isn't reachable. Creating a note in a notebook that doesn't exist yet creates it automatically, including nested `Parent/Child` paths.

### Syncing multiple sources at once

`sync_sources` pulls from several sources into one combined, browsable cache in a single `notectl sync` — useful if you want your Apple Notes and Joplin notebooks searchable together, for example:

```yaml
source: apple          # still the only write target — new notes always go here
sync_sources: apple,joplin
joplin_token: <token>
```

The cache tags each note by its real source and only clears/re-indexes that source's rows per sync, so combining sources here is safe — nothing from one source gets clobbered by syncing another. `source` itself is unaffected: it's still the one place `write`/`write_note`/the TUI's `n` create a new note.

---

## MCP — AI Integration

notectl ships an MCP server that lets Claude (and any MCP-compatible agent) read, write, and search your notes over stdio.

### Claude Desktop config

`~/Library/Application Support/Claude/claude_desktop_config.json`

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

Restart Claude Desktop after editing this file.

### MCP tools

| Tool | Description |
|------|-------------|
| `list_notes` | List notes from cache. Filters: `folder`, `source`, `limit`. |
| `read_note` | Return full content of a note by title. |
| `write_note` | Create or update a note in the vault. Params: `title`, `body`, `folder`, `tags`. |
| `search_notes` | Keyword search across titles and bodies, returns results with previews. |
| `sync_notes` | Re-index the vault into the SQLite cache. |
| `get_daily_note` | Return today's daily note; creates it from the template if it does not exist. |
| `append_daily_note` | Append content under a named section. Params: `content`, `section`, `folder`. |

### AI workflow examples

**Capture a meeting note**

> "Create a note called 'Design review 2026-07-04' in the Meetings folder with the following content: ..."

Claude calls `write_note` with `title`, `body`, `folder: Meetings`, and any relevant tags. The note is written directly to the vault and available in the TUI immediately after the next sync.

**Build a knowledge base via AI**

> "Search my notes for everything about authentication, then summarise the key decisions."

Claude calls `search_notes` with the query, then calls `read_note` for each relevant result, and synthesises the output. Optionally writes a new summary note back via `write_note`.

**Daily note workflow with `append_daily_note`**

> "Add 'Reviewed PR #42' to the Log section of today's daily note."

Claude calls `get_daily_note` (creating it from the template if today's note is missing), then calls `append_daily_note` with `section: Log` and the supplied content. The entry is appended under the correct heading without overwriting the rest of the note.

---

## Architecture

```
Obsidian vault (.md files)  ─┐
Apple Notes (AppleScript)   ─┼─ notectl sync --> SQLite (~/.local/share/notectl/notes.db)
Joplin (local Data API)     ─┘                        |-- notectl tui   (Bubbletea full-screen TUI)
                                                        |-- notectl mcp   (stdio MCP server for AI agents)
```

Each configured source is the source of truth for its own notes. The SQLite cache is a read/write mirror, tagged per source: reads are served from the cache for speed; writes go to the real source first (the vault file, Apple Notes, or Joplin — whichever `source` points at) and are reflected in the cache on the next sync, or immediately when written via the TUI or CLI. `sync_sources` can combine more than one source's notes into the same cache — see [Syncing multiple sources at once](#syncing-multiple-sources-at-once).

---

## Requirements

- macOS or Linux (Apple Notes requires macOS; Obsidian, Joplin, and plain Markdown work on both)
- Go 1.21+
- One of: an Obsidian vault (or any folder of `.md` files), Apple Notes, or a running Joplin instance with Web Clipper enabled
