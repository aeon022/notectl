# Bidirectional Mirror Sync (Apple Notes ↔ Obsidian) — Design

Status: approved, ready for implementation plan
Date: 2026-08-17

## Problem

`notectl` already supports reading multiple sources into one combined,
searchable cache via `sync_sources` (e.g. `apple,obsidian`), but `source`
remains the single write target — new/edited notes only ever go to one
backend. There is no way to keep an Apple Notes account and an Obsidian
vault mirrored: a note created or edited in one does not appear in the
other.

Goal: opt-in, true bidirectional sync between exactly one Apple Notes
account (the configured default account) and one Obsidian vault. Notes
created, edited, or deleted on either side propagate to the other,
with an explicit conflict rule and a safety gate on deletions.

## Non-goals

- Mirroring Joplin or plain-markdown sources — Apple↔Obsidian only, for now.
- Mirroring more than one Apple Notes account.
- Real-time/background sync — this rides on the existing `sync` trigger
  (CLI, TUI `s` key, MCP `sync_notes`), not a new daemon or file watcher.
- Automatic deletion propagation without confirmation.

## Architecture

New package `internal/mirror`, invoked as an additional step after the
existing per-source sync loop (`config.SyncSources()` in `cmd/sync.go`
and the TUI/MCP equivalents) — same trigger points, no new entrypoint.

Opt-in via a new config key, kept separate from `sync_sources` (which
stays a read-only combine and is unaffected for existing users):

```yaml
source: apple
sync_sources: apple,obsidian
mirror_apple_obsidian: true
vault_path: /Volumes/Resources/Dropbox/Apps/Obsidian/gerwin-vault
```

`mirror` only runs when `mirror_apple_obsidian: true` AND `sync_sources`
contains both `apple` and `obsidian`; otherwise it's a no-op. `notectl
doctor` gains a check that warns if `mirror_apple_obsidian` is set but
`sync_sources` doesn't cover both sources.

`mirror` reuses existing write/read primitives — it does not reimplement
note I/O or format conversion:

- `notes.WriteApple` / `notes.UpsertApple` / `notes.DeleteApple`
- `notes.Write` / `notes.Delete` (obsidian)
- `notes.TextToHTML` (markdown → Apple Notes rich text)
- `notes.StripHTML` (Apple Notes → markdown, already used for the TUI
  preview roundtrip)
- `appleFolderRefChain` (resolves/creates nested Apple Notes folders
  from a `"Parent/Child"`-style path string)

The Apple Notes side always uses the `"default account"` convention
already used by `WriteApple`/`UpsertApple` when no account is given —
no new account config.

## Data model

New table in the existing per-path SQLite cache (`internal/store`),
added via the existing `addColumnIfMissing`/`CREATE TABLE IF NOT
EXISTS` migration pattern in `store.migrate()`:

```sql
CREATE TABLE IF NOT EXISTS mirror_links (
    apple_id      TEXT NOT NULL UNIQUE,
    apple_hash    TEXT NOT NULL,
    obsidian_id   TEXT NOT NULL UNIQUE,
    obsidian_hash TEXT NOT NULL,
    last_synced   TEXT NOT NULL
);
```

`*_hash` is `sha256(title + "\x00" + body)` computed at the moment a
pair was last reconciled — not `mod_time`. Apple Notes' AppleScript
modification date and the vault file's mtime aren't reliably
comparable (timezone/granularity drift), so hash is the source of
truth for "did this side change since the last mirror sync". `mod_time`
is only consulted to break a genuine conflict (both sides changed).

A pending-deletion queue is a second small table:

```sql
CREATE TABLE IF NOT EXISTS mirror_pending_deletes (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    apple_id    TEXT NOT NULL DEFAULT '',
    obsidian_id TEXT NOT NULL DEFAULT '',
    title       TEXT NOT NULL,
    deleted_on  TEXT NOT NULL,   -- 'apple' | 'obsidian'
    detected_at TEXT NOT NULL
);
```

## Algorithm (runs once per `notectl sync`, after both sources are
freshly listed)

Inputs: the current `[]models.Note` from `notes.ListApple` and
`notes.List(vaultPath)` (already fetched by the existing sync loop),
plus the current `mirror_links` rows.

1. **Bootstrap-link**: for every Apple note and every Obsidian note not
   yet present in `mirror_links`, link pairs whose *translated folder
   path + title* match exactly. If their bodies already differ at this
   point, treat it exactly like a same-sync conflict (step 4) rather
   than special-casing first contact.
2. **One-sided creation**: an unlinked Apple note with no Obsidian
   match → create it in the vault (`notes.Write`, folder translated
   1:1, e.g. Apple folder `Projects/Foo` → vault subfolder
   `Projects/Foo`) and link it. Symmetric for an unlinked Obsidian note
   → `notes.UpsertApple` with `appleFolderRefChain`-resolved folder.
3. **One-sided change**: for a linked pair, if only one side's current
   hash differs from the stored `*_hash`, push that side's
   title/body/folder to the other side, update both stored hashes and
   `last_synced`.
4. **Conflict**: if both sides' current hashes differ from their stored
   values, last-write-wins by `ModTime` — the newer side's content
   overwrites the older side. Both stored hashes updated to match the
   winning content.
5. **Deletion detection**: for a linked pair, if one side's ID no
   longer appears in that side's current list, do **not** delete the
   other side. Instead upsert a row into `mirror_pending_deletes` and
   remove the pair from `mirror_links` (it's no longer an active link).
   Duplicate detection (same pair already queued) is a no-op.

This step runs identically whether triggered from CLI `sync`, the TUI's
`s` key, or the MCP `sync_notes` tool — same function, same table
writes.

## Deletion confirmation

Deletions are never auto-applied, in any calling context (CLI, TUI,
MCP) — MCP in particular has no way to prompt interactively.

- `notectl sync` prints a one-line summary of any newly-queued pending
  deletes: `2 notes deleted on Apple side — mirrored Obsidian copies
  still present. Run 'notectl sync --apply-deletes' to remove them.`
- New flag: `notectl sync --apply-deletes` — deletes every row
  currently in `mirror_pending_deletes` from the *surviving* side
  (calls `notes.DeleteApple`/`notes.Delete` as appropriate), then
  clears those rows.
- `notectl doctor` reports the current pending-delete count so it's
  visible without re-running sync.
- The `sync_notes` MCP tool's response text includes the same pending
  count/summary, so an agent can surface it to the user, but the agent
  never calls apply-deletes itself — that stays a human-initiated CLI
  action.

## Error handling

- AppleScript or filesystem write failures during mirror propagation
  (step 2/3/4) are reported per-note (title + error) and do not abort
  the rest of the mirror pass — one bad note shouldn't block every
  other pending change. The failing pair's stored hash is left
  unchanged so it's retried on the next sync.
- If `vault_path` or the Apple `default account` is unreachable at the
  start of the mirror step, it fails the same way the existing
  per-source sync already does (surfaced error, no partial mirror
  table writes).

## Testing

Table-driven unit test for the link/diff/conflict/delete algorithm
(`internal/mirror/mirror_test.go`) using fake in-memory
`[]models.Note` inputs and a fake link-store — no real Apple Notes or
filesystem required. Cases: new-on-apple-only, new-on-obsidian-only,
changed-apple-only, changed-obsidian-only, changed-both (verify newer
`ModTime` wins), deleted-on-apple (verify queued in
`mirror_pending_deletes`, not deleted), deleted-on-obsidian, and
bootstrap-link matching by folder+title.

## Open items deferred (not blocking this design)

- Rename handling: once linked, a rename on one side is just a
  "changed" hash (title is part of the hash) and propagates normally —
  no special case needed, already covered by step 3/4.
- What happens if the same title+folder appears twice on one side
  (duplicate titles) — bootstrap-link picks the first unlinked match;
  this is a pre-existing ambiguity in the codebase (see
  `store.FindByTitle`'s doc comment) and not something this feature
  needs to solve differently.
