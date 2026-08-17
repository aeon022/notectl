# Apple Notes ↔ Obsidian Mirror Sync Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add opt-in bidirectional mirroring between one Apple Notes account (the default account) and the configured Obsidian vault, riding on the existing `notectl sync` trigger (CLI, TUI `s`, MCP `sync_notes`).

**Architecture:** A pure, store-free decision function (`mirror.Decide`) diffs the current Apple Notes list, the current vault note list, and a persisted link table to produce create/push/delete-queue decisions; a thin orchestration function (`mirror.Sync`) applies those decisions using notes.go's existing Apple/Obsidian read-write primitives and persists the link table via two new SQLite tables.

**Tech Stack:** Go, existing `internal/notes` (AppleScript + filesystem), existing `internal/store` (modernc.org/sqlite), Cobra CLI.

**Spec:** `docs/superpowers/specs/2026-08-17-mirror-sync-design.md`

## Global Constraints

- Opt-in only via `mirror_apple_obsidian: true`; requires `sync_sources` to include both `apple` and an obsidian-backed source (`obsidian` or `markdown`) — otherwise a no-op, flagged by `doctor`.
- Mirrors exactly one Apple Notes account: the `"default account"` — same convention `WriteApple`/`UpsertApple` already use.
- No daemon, no file watcher — the mirror step runs synchronously inside the existing `sync` trigger (CLI `notectl sync`, TUI `s` key, MCP `sync_notes`), once per invocation.
- Deletions are never auto-applied in any calling context. They're queued in `mirror_pending_deletes` and only removed by the explicit `notectl sync --apply-deletes` CLI command.
- Conflict rule: when a linked pair changed on both sides since the last mirror pass, the side with the newer `ModTime` wins and overwrites the other.
- Folder mapping is the literal `Folder` string on both sides (e.g. `"Projects/Foo"`) — no translation table, since both `notes.Write` and `notes.WriteApple`/`appleFolderRefChain` already accept that same path-like format natively.

---

## Task 1: Store schema and CRUD for mirror link/pending-delete tables

**Files:**
- Modify: `internal/store/sqlite.go` (the `migrate()` function, ~line 110-140)
- Create: `internal/store/mirror.go`
- Create: `internal/store/mirror_test.go`

**Interfaces:**
- Produces (used by Task 3): `store.MirrorLink{AppleID, AppleHash, ObsidianID, ObsidianHash string; LastSynced time.Time}`, `store.MirrorPendingDelete{ID int64; AppleID, ObsidianID, Title, DeletedOn string; DetectedAt time.Time}`, and methods `(*Store) ListMirrorLinks(ctx) ([]MirrorLink, error)`, `(*Store) UpsertMirrorLink(ctx, MirrorLink) error`, `(*Store) DeleteMirrorLink(ctx, appleID string) error`, `(*Store) ListMirrorPendingDeletes(ctx) ([]MirrorPendingDelete, error)`, `(*Store) AddMirrorPendingDelete(ctx, MirrorPendingDelete) error`, `(*Store) DeleteMirrorPendingDelete(ctx, id int64) error`, `(*Store) CountMirrorPendingDeletes(ctx) (int, error)`.

- [ ] **Step 1: Add the two new tables to `migrate()`**

In `internal/store/sqlite.go`, inside the SQL string passed to `s.db.Exec` in `migrate()` (right after the existing `folders` table's closing `);`), add:

```sql

		CREATE TABLE IF NOT EXISTS mirror_links (
			apple_id      TEXT PRIMARY KEY,
			apple_hash    TEXT NOT NULL,
			obsidian_id   TEXT NOT NULL UNIQUE,
			obsidian_hash TEXT NOT NULL,
			last_synced   TEXT NOT NULL
		);

		CREATE TABLE IF NOT EXISTS mirror_pending_deletes (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			apple_id    TEXT NOT NULL DEFAULT '',
			obsidian_id TEXT NOT NULL DEFAULT '',
			title       TEXT NOT NULL,
			deleted_on  TEXT NOT NULL,
			detected_at TEXT NOT NULL,
			UNIQUE(apple_id, obsidian_id)
		);
```

`apple_id` is the primary key (not just unique) because a pair mirrors exactly one Apple note to exactly one Obsidian file — re-upserting a link for the same Apple note (e.g. after a title change moved the Obsidian file to a new path/ID) updates the same row in place instead of leaving a stale one behind.

- [ ] **Step 2: Write `internal/store/mirror.go`**

```go
package store

import (
	"context"
	"time"
)

type MirrorLink struct {
	AppleID      string
	AppleHash    string
	ObsidianID   string
	ObsidianHash string
	LastSynced   time.Time
}

type MirrorPendingDelete struct {
	ID         int64
	AppleID    string
	ObsidianID string
	Title      string
	DeletedOn  string // "apple" | "obsidian" — which side the note is gone from
	DetectedAt time.Time
}

func (s *Store) ListMirrorLinks(ctx context.Context) ([]MirrorLink, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT apple_id, apple_hash, obsidian_id, obsidian_hash, last_synced FROM mirror_links`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var links []MirrorLink
	for rows.Next() {
		var l MirrorLink
		var lastSynced string
		if err := rows.Scan(&l.AppleID, &l.AppleHash, &l.ObsidianID, &l.ObsidianHash, &lastSynced); err != nil {
			return nil, err
		}
		l.LastSynced, _ = time.Parse(time.RFC3339, lastSynced)
		links = append(links, l)
	}
	return links, rows.Err()
}

// UpsertMirrorLink inserts or replaces the link row for l.AppleID — a
// second upsert for the same AppleID (e.g. its Obsidian counterpart got a
// new ID after a rename moved it to a new file path) updates the existing
// row rather than leaving the old ObsidianID behind as an orphan.
func (s *Store) UpsertMirrorLink(ctx context.Context, l MirrorLink) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO mirror_links (apple_id, apple_hash, obsidian_id, obsidian_hash, last_synced)
		VALUES (?,?,?,?,?)
		ON CONFLICT(apple_id) DO UPDATE SET
			apple_hash=excluded.apple_hash, obsidian_id=excluded.obsidian_id,
			obsidian_hash=excluded.obsidian_hash, last_synced=excluded.last_synced
	`, l.AppleID, l.AppleHash, l.ObsidianID, l.ObsidianHash, l.LastSynced.UTC().Format(time.RFC3339))
	return err
}

func (s *Store) DeleteMirrorLink(ctx context.Context, appleID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM mirror_links WHERE apple_id=?`, appleID)
	return err
}

func (s *Store) ListMirrorPendingDeletes(ctx context.Context) ([]MirrorPendingDelete, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, apple_id, obsidian_id, title, deleted_on, detected_at FROM mirror_pending_deletes ORDER BY detected_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MirrorPendingDelete
	for rows.Next() {
		var p MirrorPendingDelete
		var detectedAt string
		if err := rows.Scan(&p.ID, &p.AppleID, &p.ObsidianID, &p.Title, &p.DeletedOn, &detectedAt); err != nil {
			return nil, err
		}
		p.DetectedAt, _ = time.Parse(time.RFC3339, detectedAt)
		out = append(out, p)
	}
	return out, rows.Err()
}

// AddMirrorPendingDelete is a no-op if this exact (apple_id, obsidian_id)
// pair is already queued — so re-detecting the same deletion on every sync
// before it's applied doesn't pile up duplicate rows.
func (s *Store) AddMirrorPendingDelete(ctx context.Context, p MirrorPendingDelete) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO mirror_pending_deletes (apple_id, obsidian_id, title, deleted_on, detected_at)
		VALUES (?,?,?,?,?)
	`, p.AppleID, p.ObsidianID, p.Title, p.DeletedOn, p.DetectedAt.UTC().Format(time.RFC3339))
	return err
}

func (s *Store) DeleteMirrorPendingDelete(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM mirror_pending_deletes WHERE id=?`, id)
	return err
}

func (s *Store) CountMirrorPendingDeletes(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM mirror_pending_deletes`).Scan(&n)
	return n, err
}
```

- [ ] **Step 3: Write `internal/store/mirror_test.go`**

```go
package store

import (
	"context"
	"testing"
	"time"
)

func TestUpsertMirrorLink_RoundTrips(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	link := MirrorLink{AppleID: "apple-1", AppleHash: "h1", ObsidianID: "obs-1", ObsidianHash: "h1", LastSynced: time.Now()}
	if err := s.UpsertMirrorLink(ctx, link); err != nil {
		t.Fatalf("UpsertMirrorLink() error = %v", err)
	}
	links, err := s.ListMirrorLinks(ctx)
	if err != nil {
		t.Fatalf("ListMirrorLinks() error = %v", err)
	}
	if len(links) != 1 || links[0].AppleID != "apple-1" || links[0].ObsidianID != "obs-1" {
		t.Fatalf("ListMirrorLinks() = %+v, want one link apple-1/obs-1", links)
	}
}

// A rename on the Obsidian side gives that note a new ID (it's a hash of
// the file's relative path). Re-upserting under the same AppleID must
// update the existing row's ObsidianID in place, not leave the old one
// behind as an orphan.
func TestUpsertMirrorLink_UpdatesObsidianIDInPlace(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	_ = s.UpsertMirrorLink(ctx, MirrorLink{AppleID: "apple-1", AppleHash: "h1", ObsidianID: "obs-1", ObsidianHash: "h1", LastSynced: time.Now()})
	if err := s.UpsertMirrorLink(ctx, MirrorLink{AppleID: "apple-1", AppleHash: "h2", ObsidianID: "obs-2", ObsidianHash: "h2", LastSynced: time.Now()}); err != nil {
		t.Fatalf("second UpsertMirrorLink() error = %v", err)
	}
	links, err := s.ListMirrorLinks(ctx)
	if err != nil {
		t.Fatalf("ListMirrorLinks() error = %v", err)
	}
	if len(links) != 1 {
		t.Fatalf("want 1 link after re-upsert, got %d", len(links))
	}
	if links[0].ObsidianID != "obs-2" {
		t.Fatalf("ObsidianID = %q, want obs-2", links[0].ObsidianID)
	}
}

func TestDeleteMirrorLink_Removes(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	_ = s.UpsertMirrorLink(ctx, MirrorLink{AppleID: "apple-1", AppleHash: "h1", ObsidianID: "obs-1", ObsidianHash: "h1", LastSynced: time.Now()})
	if err := s.DeleteMirrorLink(ctx, "apple-1"); err != nil {
		t.Fatalf("DeleteMirrorLink() error = %v", err)
	}
	links, err := s.ListMirrorLinks(ctx)
	if err != nil {
		t.Fatalf("ListMirrorLinks() error = %v", err)
	}
	if len(links) != 0 {
		t.Fatalf("want 0 links after delete, got %d", len(links))
	}
}

func TestMirrorPendingDeletes_AddListCountDelete(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	p := MirrorPendingDelete{AppleID: "apple-1", ObsidianID: "obs-1", Title: "Groceries", DeletedOn: "apple", DetectedAt: time.Now()}
	if err := s.AddMirrorPendingDelete(ctx, p); err != nil {
		t.Fatalf("AddMirrorPendingDelete() error = %v", err)
	}
	// Re-detecting the same pending delete before it's applied must not
	// create a second row.
	if err := s.AddMirrorPendingDelete(ctx, p); err != nil {
		t.Fatalf("duplicate AddMirrorPendingDelete() error = %v", err)
	}
	pending, err := s.ListMirrorPendingDeletes(ctx)
	if err != nil {
		t.Fatalf("ListMirrorPendingDeletes() error = %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("want 1 pending delete (deduped), got %d", len(pending))
	}
	n, err := s.CountMirrorPendingDeletes(ctx)
	if err != nil {
		t.Fatalf("CountMirrorPendingDeletes() error = %v", err)
	}
	if n != 1 {
		t.Fatalf("CountMirrorPendingDeletes() = %d, want 1", n)
	}
	if err := s.DeleteMirrorPendingDelete(ctx, pending[0].ID); err != nil {
		t.Fatalf("DeleteMirrorPendingDelete() error = %v", err)
	}
	n, err = s.CountMirrorPendingDeletes(ctx)
	if err != nil {
		t.Fatalf("CountMirrorPendingDeletes() error = %v", err)
	}
	if n != 0 {
		t.Fatalf("CountMirrorPendingDeletes() after delete = %d, want 0", n)
	}
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/store/... -run TestUpsertMirrorLink -run TestDeleteMirrorLink -run TestMirrorPendingDeletes -v`
(or simply `go test ./internal/store/... -v` to run the whole package)
Expected: PASS for all four new tests, no regressions in existing store tests.

- [ ] **Step 5: Commit**

```bash
git add internal/store/sqlite.go internal/store/mirror.go internal/store/mirror_test.go
git commit -m "Add mirror_links and mirror_pending_deletes tables with CRUD"
```

---

## Task 2: Pure decision algorithm (`mirror.Decide`)

**Files:**
- Create: `internal/mirror/decide.go`
- Create: `internal/mirror/decide_test.go`

**Interfaces:**
- Consumes: `models.Note{ID, Title, Body string; Tags []string; Folder, Account, Path, Source string; ModTime, Created time.Time}` (from `internal/models`).
- Produces (used by Task 3): `mirror.Link{AppleID, AppleHash, ObsidianID, ObsidianHash string}`, `mirror.Push{Title, Body, Folder string; Link Link}`, `mirror.PendingDelete{AppleID, ObsidianID, Title, DeletedOn string}`, `mirror.Decisions{CreateInObsidian, CreateInApple []models.Note; PushToApple, PushToObsidian []Push; NewLinks []Link; NewPendingDeletes []PendingDelete; RemoveLinks []Link}`, `mirror.Hash(title, body string) string`, `mirror.Decide(appleNotes, obsidianNotes []models.Note, links []Link) Decisions`.

- [ ] **Step 1: Write the failing tests**

Create `internal/mirror/decide_test.go`:

```go
package mirror

import (
	"testing"
	"time"

	"github.com/aeon022/notectl/internal/models"
)

var fixedTime = time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)

func TestDecide_NewAppleNoteCreatesInObsidian(t *testing.T) {
	apple := []models.Note{{ID: "apple-1", Title: "Groceries", Body: "milk", Folder: "Shopping", ModTime: fixedTime}}
	d := Decide(apple, nil, nil)
	if len(d.CreateInObsidian) != 1 || d.CreateInObsidian[0].ID != "apple-1" {
		t.Fatalf("CreateInObsidian = %+v, want the apple note", d.CreateInObsidian)
	}
	if len(d.CreateInApple) != 0 || len(d.PushToApple) != 0 || len(d.PushToObsidian) != 0 || len(d.NewLinks) != 0 {
		t.Fatalf("unexpected extra decisions: %+v", d)
	}
}

func TestDecide_NewObsidianNoteCreatesInApple(t *testing.T) {
	obs := []models.Note{{ID: "obs-1", Title: "Groceries", Body: "milk", Folder: "Shopping", ModTime: fixedTime}}
	d := Decide(nil, obs, nil)
	if len(d.CreateInApple) != 1 || d.CreateInApple[0].ID != "obs-1" {
		t.Fatalf("CreateInApple = %+v, want the obsidian note", d.CreateInApple)
	}
	if len(d.CreateInObsidian) != 0 || len(d.PushToApple) != 0 || len(d.PushToObsidian) != 0 || len(d.NewLinks) != 0 {
		t.Fatalf("unexpected extra decisions: %+v", d)
	}
}

func TestDecide_BootstrapLinksMatchingTitleAndFolder(t *testing.T) {
	apple := []models.Note{{ID: "apple-1", Title: "Groceries", Body: "milk", Folder: "Shopping", ModTime: fixedTime}}
	obs := []models.Note{{ID: "obs-1", Title: "Groceries", Body: "milk", Folder: "Shopping", ModTime: fixedTime}}
	d := Decide(apple, obs, nil)
	if len(d.NewLinks) != 1 {
		t.Fatalf("NewLinks = %+v, want 1", d.NewLinks)
	}
	l := d.NewLinks[0]
	if l.AppleID != "apple-1" || l.ObsidianID != "obs-1" {
		t.Fatalf("NewLinks[0] = %+v, want apple-1/obs-1", l)
	}
	if len(d.CreateInApple) != 0 || len(d.CreateInObsidian) != 0 || len(d.PushToApple) != 0 || len(d.PushToObsidian) != 0 {
		t.Fatalf("unexpected extra decisions: %+v", d)
	}
}

func TestDecide_BootstrapConflictNewerWins(t *testing.T) {
	older := fixedTime
	newer := fixedTime.Add(time.Hour)
	apple := []models.Note{{ID: "apple-1", Title: "Groceries", Body: "milk", Folder: "Shopping", ModTime: newer}}
	obs := []models.Note{{ID: "obs-1", Title: "Groceries", Body: "eggs", Folder: "Shopping", ModTime: older}}
	d := Decide(apple, obs, nil)
	if len(d.PushToObsidian) != 1 {
		t.Fatalf("PushToObsidian = %+v, want 1 (apple is newer)", d.PushToObsidian)
	}
	p := d.PushToObsidian[0]
	if p.Body != "milk" || p.Link.AppleID != "apple-1" || p.Link.ObsidianID != "obs-1" {
		t.Fatalf("PushToObsidian[0] = %+v, want body=milk linking apple-1/obs-1", p)
	}
	if len(d.NewLinks) != 0 || len(d.PushToApple) != 0 {
		t.Fatalf("unexpected extra decisions: %+v", d)
	}
}

func TestDecide_ChangedOnAppleOnlyPushesToObsidian(t *testing.T) {
	link := Link{AppleID: "apple-1", AppleHash: Hash("Groceries", "milk"), ObsidianID: "obs-1", ObsidianHash: Hash("Groceries", "milk")}
	apple := []models.Note{{ID: "apple-1", Title: "Groceries", Body: "milk and eggs", Folder: "Shopping", ModTime: fixedTime}}
	obs := []models.Note{{ID: "obs-1", Title: "Groceries", Body: "milk", Folder: "Shopping", ModTime: fixedTime}}
	d := Decide(apple, obs, []Link{link})
	if len(d.PushToObsidian) != 1 || d.PushToObsidian[0].Body != "milk and eggs" {
		t.Fatalf("PushToObsidian = %+v, want 1 pushing 'milk and eggs'", d.PushToObsidian)
	}
	if len(d.PushToApple) != 0 || len(d.CreateInApple) != 0 || len(d.CreateInObsidian) != 0 {
		t.Fatalf("unexpected extra decisions: %+v", d)
	}
}

func TestDecide_ChangedOnObsidianOnlyPushesToApple(t *testing.T) {
	link := Link{AppleID: "apple-1", AppleHash: Hash("Groceries", "milk"), ObsidianID: "obs-1", ObsidianHash: Hash("Groceries", "milk")}
	apple := []models.Note{{ID: "apple-1", Title: "Groceries", Body: "milk", Folder: "Shopping", ModTime: fixedTime}}
	obs := []models.Note{{ID: "obs-1", Title: "Groceries", Body: "milk and eggs", Folder: "Shopping", ModTime: fixedTime}}
	d := Decide(apple, obs, []Link{link})
	if len(d.PushToApple) != 1 || d.PushToApple[0].Body != "milk and eggs" {
		t.Fatalf("PushToApple = %+v, want 1 pushing 'milk and eggs'", d.PushToApple)
	}
	if len(d.PushToObsidian) != 0 || len(d.CreateInApple) != 0 || len(d.CreateInObsidian) != 0 {
		t.Fatalf("unexpected extra decisions: %+v", d)
	}
}

func TestDecide_ChangedOnBothSidesConflictNewerWins(t *testing.T) {
	link := Link{AppleID: "apple-1", AppleHash: Hash("Groceries", "milk"), ObsidianID: "obs-1", ObsidianHash: Hash("Groceries", "milk")}
	older := fixedTime
	newer := fixedTime.Add(time.Hour)
	apple := []models.Note{{ID: "apple-1", Title: "Groceries", Body: "milk and eggs", Folder: "Shopping", ModTime: older}}
	obs := []models.Note{{ID: "obs-1", Title: "Groceries", Body: "milk and bread", Folder: "Shopping", ModTime: newer}}
	d := Decide(apple, obs, []Link{link})
	if len(d.PushToApple) != 1 || d.PushToApple[0].Body != "milk and bread" {
		t.Fatalf("PushToApple = %+v, want 1 pushing the newer obsidian body", d.PushToApple)
	}
	if len(d.PushToObsidian) != 0 {
		t.Fatalf("PushToObsidian = %+v, want 0 (obsidian was newer)", d.PushToObsidian)
	}
}

func TestDecide_UnchangedPairProducesNoDecisions(t *testing.T) {
	link := Link{AppleID: "apple-1", AppleHash: Hash("Groceries", "milk"), ObsidianID: "obs-1", ObsidianHash: Hash("Groceries", "milk")}
	apple := []models.Note{{ID: "apple-1", Title: "Groceries", Body: "milk", Folder: "Shopping", ModTime: fixedTime}}
	obs := []models.Note{{ID: "obs-1", Title: "Groceries", Body: "milk", Folder: "Shopping", ModTime: fixedTime}}
	d := Decide(apple, obs, []Link{link})
	if len(d.PushToApple) != 0 || len(d.PushToObsidian) != 0 || len(d.CreateInApple) != 0 ||
		len(d.CreateInObsidian) != 0 || len(d.NewLinks) != 0 || len(d.NewPendingDeletes) != 0 {
		t.Fatalf("want no decisions for an unchanged pair, got %+v", d)
	}
}

func TestDecide_DeletedOnAppleQueuesPendingDelete(t *testing.T) {
	link := Link{AppleID: "apple-1", AppleHash: Hash("Groceries", "milk"), ObsidianID: "obs-1", ObsidianHash: Hash("Groceries", "milk")}
	obs := []models.Note{{ID: "obs-1", Title: "Groceries", Body: "milk", Folder: "Shopping", ModTime: fixedTime}}
	d := Decide(nil, obs, []Link{link}) // apple-1 no longer present
	if len(d.NewPendingDeletes) != 1 || d.NewPendingDeletes[0].DeletedOn != "apple" {
		t.Fatalf("NewPendingDeletes = %+v, want 1 with DeletedOn=apple", d.NewPendingDeletes)
	}
	if len(d.RemoveLinks) != 1 || d.RemoveLinks[0].AppleID != "apple-1" {
		t.Fatalf("RemoveLinks = %+v, want the apple-1/obs-1 link", d.RemoveLinks)
	}
	if len(d.PushToApple) != 0 || len(d.PushToObsidian) != 0 {
		t.Fatalf("must not push when a side was deleted: %+v", d)
	}
}

func TestDecide_DeletedOnObsidianQueuesPendingDelete(t *testing.T) {
	link := Link{AppleID: "apple-1", AppleHash: Hash("Groceries", "milk"), ObsidianID: "obs-1", ObsidianHash: Hash("Groceries", "milk")}
	apple := []models.Note{{ID: "apple-1", Title: "Groceries", Body: "milk", Folder: "Shopping", ModTime: fixedTime}}
	d := Decide(apple, nil, []Link{link}) // obs-1 no longer present
	if len(d.NewPendingDeletes) != 1 || d.NewPendingDeletes[0].DeletedOn != "obsidian" {
		t.Fatalf("NewPendingDeletes = %+v, want 1 with DeletedOn=obsidian", d.NewPendingDeletes)
	}
	if len(d.RemoveLinks) != 1 {
		t.Fatalf("RemoveLinks = %+v, want 1", d.RemoveLinks)
	}
}

func TestDecide_BothSidesDeletedDropsLinkSilently(t *testing.T) {
	link := Link{AppleID: "apple-1", AppleHash: Hash("Groceries", "milk"), ObsidianID: "obs-1", ObsidianHash: Hash("Groceries", "milk")}
	d := Decide(nil, nil, []Link{link})
	if len(d.NewPendingDeletes) != 0 {
		t.Fatalf("NewPendingDeletes = %+v, want 0 when both sides are already gone", d.NewPendingDeletes)
	}
	if len(d.RemoveLinks) != 1 {
		t.Fatalf("RemoveLinks = %+v, want the stale link removed", d.RemoveLinks)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/mirror/... -v`
Expected: FAIL — build error, `Decide`/`Link`/`Hash`/etc. undefined (the package doesn't exist yet).

- [ ] **Step 3: Write `internal/mirror/decide.go`**

```go
// Package mirror computes and applies a bidirectional sync between one
// Apple Notes account and one Obsidian vault. Decide is a pure function —
// no I/O, no Apple Notes, no filesystem — so the linking/conflict logic is
// fully unit-testable; Sync (in mirror.go) is the thin orchestration layer
// that feeds it real data and applies its output.
package mirror

import (
	"crypto/sha256"
	"encoding/hex"

	"github.com/aeon022/notectl/internal/models"
)

// Link pairs one Apple Notes note with one Obsidian vault note, plus the
// content hash each side had the last time they were reconciled — that's
// what lets Decide tell "changed since last mirror sync" apart from
// "always been different", without relying on cross-platform-comparable
// modification times.
type Link struct {
	AppleID      string
	AppleHash    string
	ObsidianID   string
	ObsidianHash string
}

// Push carries the winning title/body/folder from one side of a linked
// pair, to be written to the other side.
type Push struct {
	Title  string
	Body   string
	Folder string
	Link   Link
}

// PendingDelete records that a linked note disappeared from one side and
// is awaiting a human-confirmed delete on the other — see the design's
// "Deletion confirmation" section for why this is never auto-applied.
type PendingDelete struct {
	AppleID    string
	ObsidianID string
	Title      string
	DeletedOn  string // "apple" | "obsidian"
}

type Decisions struct {
	CreateInObsidian  []models.Note // apple notes with no obsidian match; Folder already the target vault subfolder
	CreateInApple     []models.Note // obsidian notes with no apple match; Folder already the target apple folder path
	PushToApple       []Push
	PushToObsidian    []Push
	NewLinks          []Link // bootstrap matches whose content already agreed — no push needed, just record the link
	NewPendingDeletes []PendingDelete
	RemoveLinks       []Link // links to drop because one (or both) sides are gone
}

// Hash is the content fingerprint stored per side of a Link. Exported so
// callers (Sync, and tests) can compute the same value Decide uses.
func Hash(title, body string) string {
	sum := sha256.Sum256([]byte(title + "\x00" + body))
	return hex.EncodeToString(sum[:])
}

// Decide computes what a mirror pass should do, given the current note
// lists from both sides and the previously persisted links. It performs no
// I/O and mutates none of its inputs.
func Decide(appleNotes, obsidianNotes []models.Note, links []Link) Decisions {
	var d Decisions

	appleByID := indexByID(appleNotes)
	obsByID := indexByID(obsidianNotes)

	linkedApple := make(map[string]bool, len(links))
	linkedObsidian := make(map[string]bool, len(links))

	for _, l := range links {
		_, appleStill := appleByID[l.AppleID]
		_, obsStill := obsByID[l.ObsidianID]
		linkedApple[l.AppleID] = true
		linkedObsidian[l.ObsidianID] = true

		switch {
		case !appleStill && !obsStill:
			// Both sides already gone (e.g. deleted on both before this
			// pass ever ran) — nothing to queue, just drop the stale link.
			d.RemoveLinks = append(d.RemoveLinks, l)
		case !appleStill:
			d.NewPendingDeletes = append(d.NewPendingDeletes, PendingDelete{
				AppleID: l.AppleID, ObsidianID: l.ObsidianID,
				Title: obsByID[l.ObsidianID].Title, DeletedOn: "apple",
			})
			d.RemoveLinks = append(d.RemoveLinks, l)
		case !obsStill:
			d.NewPendingDeletes = append(d.NewPendingDeletes, PendingDelete{
				AppleID: l.AppleID, ObsidianID: l.ObsidianID,
				Title: appleByID[l.AppleID].Title, DeletedOn: "obsidian",
			})
			d.RemoveLinks = append(d.RemoveLinks, l)
		default:
			a, o := appleByID[l.AppleID], obsByID[l.ObsidianID]
			aChanged := Hash(a.Title, a.Body) != l.AppleHash
			oChanged := Hash(o.Title, o.Body) != l.ObsidianHash
			switch {
			case !aChanged && !oChanged:
				// nothing to do
			case aChanged && !oChanged:
				d.PushToObsidian = append(d.PushToObsidian, Push{Title: a.Title, Body: a.Body, Folder: a.Folder, Link: l})
			case oChanged && !aChanged:
				d.PushToApple = append(d.PushToApple, Push{Title: o.Title, Body: o.Body, Folder: o.Folder, Link: l})
			default: // both changed since the last mirror pass: newer ModTime wins
				if a.ModTime.After(o.ModTime) {
					d.PushToObsidian = append(d.PushToObsidian, Push{Title: a.Title, Body: a.Body, Folder: a.Folder, Link: l})
				} else {
					d.PushToApple = append(d.PushToApple, Push{Title: o.Title, Body: o.Body, Folder: o.Folder, Link: l})
				}
			}
		}
	}

	// Bootstrap: link any not-yet-linked pair that shares folder+title —
	// this is what makes a first mirror pass over two vaults/accounts that
	// already have the same note in both not create a duplicate.
	var unlinkedApple []models.Note
	for _, a := range appleNotes {
		if !linkedApple[a.ID] {
			unlinkedApple = append(unlinkedApple, a)
		}
	}
	var unlinkedObs []models.Note
	for _, o := range obsidianNotes {
		if !linkedObsidian[o.ID] {
			unlinkedObs = append(unlinkedObs, o)
		}
	}

	matchedObs := make(map[int]bool, len(unlinkedObs))
	var stillUnlinkedApple []models.Note
	for _, a := range unlinkedApple {
		match := -1
		for i, o := range unlinkedObs {
			if !matchedObs[i] && o.Folder == a.Folder && o.Title == a.Title {
				match = i
				break
			}
		}
		if match == -1 {
			stillUnlinkedApple = append(stillUnlinkedApple, a)
			continue
		}
		matchedObs[match] = true
		o := unlinkedObs[match]
		aHash, oHash := Hash(a.Title, a.Body), Hash(o.Title, o.Body)
		switch {
		case aHash == oHash:
			d.NewLinks = append(d.NewLinks, Link{AppleID: a.ID, AppleHash: aHash, ObsidianID: o.ID, ObsidianHash: oHash})
		case a.ModTime.After(o.ModTime):
			d.PushToObsidian = append(d.PushToObsidian, Push{Title: a.Title, Body: a.Body, Folder: a.Folder, Link: Link{AppleID: a.ID, ObsidianID: o.ID}})
		default:
			d.PushToApple = append(d.PushToApple, Push{Title: o.Title, Body: o.Body, Folder: o.Folder, Link: Link{AppleID: a.ID, ObsidianID: o.ID}})
		}
	}

	var stillUnlinkedObs []models.Note
	for i, o := range unlinkedObs {
		if !matchedObs[i] {
			stillUnlinkedObs = append(stillUnlinkedObs, o)
		}
	}

	d.CreateInObsidian = append(d.CreateInObsidian, stillUnlinkedApple...)
	d.CreateInApple = append(d.CreateInApple, stillUnlinkedObs...)

	return d
}

func indexByID(notes []models.Note) map[string]models.Note {
	m := make(map[string]models.Note, len(notes))
	for _, n := range notes {
		m[n.ID] = n
	}
	return m
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/mirror/... -v`
Expected: PASS for all 11 tests.

- [ ] **Step 5: Commit**

```bash
git add internal/mirror/decide.go internal/mirror/decide_test.go
git commit -m "Add pure mirror-sync decision algorithm with unit tests"
```

---

## Task 3: Orchestration (`mirror.Sync` and `mirror.ApplyPendingDeletes`)

**Files:**
- Create: `internal/mirror/mirror.go`

**Interfaces:**
- Consumes: everything from Task 1 (`store.MirrorLink`, `store.MirrorPendingDelete`, and the `Store` CRUD methods) and Task 2 (`Decide`, `Hash`, `Decisions`, `Link`, `Push`, `PendingDelete`); from `internal/notes`: `ListApple(folder string) ([]models.Note, error)`, `List(vaultPath string) ([]models.Note, error)`, `WriteApple(id, title, htmlBody, folder string) (string, error)`, `Write(vaultPath, title, body string, tags []string, folder string) (*models.Note, error)`, `Delete(vaultPath, relPath string) error`, `DeleteApple(id string) error`, `UpdateBody(id, htmlBody string) error`, `TextToHTML(body string) string`.
- Produces (used by Task 4): `mirror.Report{Created, Updated, LinkedExisting, PendingDeletes int; Errors []string}`, `mirror.Sync(ctx context.Context, s *store.Store, vaultPath string) (Report, error)`, `mirror.ApplyPendingDeletes(ctx context.Context, s *store.Store, vaultPath string) (applied int, errs []string, err error)`.

This task is I/O-heavy (AppleScript, filesystem) the same way `internal/notes/apple.go`'s script-running functions are — it isn't unit tested here, the same way `WriteApple`/`DeleteApple` aren't. It's exercised via the manual smoke test at the end of Task 4.

- [ ] **Step 1: Write `internal/mirror/mirror.go`**

```go
package mirror

import (
	"context"
	"fmt"
	"time"

	"github.com/aeon022/notectl/internal/notes"
	"github.com/aeon022/notectl/internal/store"
)

// Report summarizes one mirror.Sync pass for CLI/TUI/MCP output.
type Report struct {
	Created        int // new notes written to either side
	Updated        int // existing notes pushed to the other side
	LinkedExisting int // bootstrap matches that needed no content push
	PendingDeletes int // newly queued this pass — see ApplyPendingDeletes
	Errors         []string
}

// Sync runs one mirror pass: lists both sides fresh, diffs against the
// persisted link table via Decide, and applies the resulting decisions.
// Deletions are never applied here — see ApplyPendingDeletes.
func Sync(ctx context.Context, s *store.Store, vaultPath string) (Report, error) {
	var report Report

	appleNotes, err := notes.ListApple("")
	if err != nil {
		return report, fmt.Errorf("list apple notes: %w", err)
	}
	obsidianNotes, err := notes.List(vaultPath)
	if err != nil {
		return report, fmt.Errorf("list vault: %w", err)
	}

	storedLinks, err := s.ListMirrorLinks(ctx)
	if err != nil {
		return report, fmt.Errorf("load mirror links: %w", err)
	}
	links := make([]Link, len(storedLinks))
	for i, l := range storedLinks {
		links[i] = Link{AppleID: l.AppleID, AppleHash: l.AppleHash, ObsidianID: l.ObsidianID, ObsidianHash: l.ObsidianHash}
	}

	d := Decide(appleNotes, obsidianNotes, links)
	obsByID := indexByID(obsidianNotes)

	for _, n := range d.CreateInApple {
		appleID, err := notes.WriteApple("", n.Title, notes.TextToHTML(appleBody(n.Title, n.Body)), n.Folder)
		if err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("create in apple %q: %v", n.Title, err))
			continue
		}
		h := Hash(n.Title, n.Body)
		if err := s.UpsertMirrorLink(ctx, store.MirrorLink{AppleID: appleID, AppleHash: h, ObsidianID: n.ID, ObsidianHash: h, LastSynced: time.Now()}); err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("link new apple note %q: %v", n.Title, err))
			continue
		}
		report.Created++
	}

	for _, n := range d.CreateInObsidian {
		newNote, err := notes.Write(vaultPath, n.Title, n.Body, n.Tags, n.Folder)
		if err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("create in obsidian %q: %v", n.Title, err))
			continue
		}
		h := Hash(n.Title, n.Body)
		if err := s.UpsertMirrorLink(ctx, store.MirrorLink{AppleID: n.ID, AppleHash: h, ObsidianID: newNote.ID, ObsidianHash: h, LastSynced: time.Now()}); err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("link new obsidian note %q: %v", n.Title, err))
			continue
		}
		report.Created++
	}

	for _, p := range d.PushToApple {
		if err := notes.UpdateBody(p.Link.AppleID, notes.TextToHTML(appleBody(p.Title, p.Body))); err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("update apple %q: %v", p.Title, err))
			continue
		}
		h := Hash(p.Title, p.Body)
		if err := s.UpsertMirrorLink(ctx, store.MirrorLink{AppleID: p.Link.AppleID, AppleHash: h, ObsidianID: p.Link.ObsidianID, ObsidianHash: h, LastSynced: time.Now()}); err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("relink apple %q: %v", p.Title, err))
			continue
		}
		report.Updated++
	}

	for _, p := range d.PushToObsidian {
		old := obsByID[p.Link.ObsidianID] // zero value if this is a fresh bootstrap link — Tags is then just nil, which is fine
		newNote, err := notes.Write(vaultPath, p.Title, p.Body, old.Tags, p.Folder)
		if err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("update obsidian %q: %v", p.Title, err))
			continue
		}
		// A title change moves the file to a new path, hence a new ID —
		// clean up the old file so a rename doesn't leave an orphan copy.
		if old.Path != "" && newNote.Path != old.Path {
			_ = notes.Delete(vaultPath, old.Path)
		}
		h := Hash(p.Title, p.Body)
		if err := s.UpsertMirrorLink(ctx, store.MirrorLink{AppleID: p.Link.AppleID, AppleHash: h, ObsidianID: newNote.ID, ObsidianHash: h, LastSynced: time.Now()}); err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("relink obsidian %q: %v", p.Title, err))
			continue
		}
		report.Updated++
	}

	for _, l := range d.NewLinks {
		if err := s.UpsertMirrorLink(ctx, store.MirrorLink{AppleID: l.AppleID, AppleHash: l.AppleHash, ObsidianID: l.ObsidianID, ObsidianHash: l.ObsidianHash, LastSynced: time.Now()}); err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("link existing pair %s/%s: %v", l.AppleID, l.ObsidianID, err))
			continue
		}
		report.LinkedExisting++
	}

	for _, l := range d.RemoveLinks {
		_ = s.DeleteMirrorLink(ctx, l.AppleID)
	}

	for _, p := range d.NewPendingDeletes {
		if err := s.AddMirrorPendingDelete(ctx, store.MirrorPendingDelete{
			AppleID: p.AppleID, ObsidianID: p.ObsidianID, Title: p.Title, DeletedOn: p.DeletedOn, DetectedAt: time.Now(),
		}); err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("queue pending delete %q: %v", p.Title, err))
			continue
		}
		report.PendingDeletes++
	}

	return report, nil
}

// appleBody prefixes title into body the same way notes.UpsertApple's
// update path does — WriteApple/UpdateBody's HTML body needs the title as
// its own first line, or Apple Notes displays a blank/mismatched title.
func appleBody(title, body string) string {
	if body == "" {
		return title
	}
	return title + "\n\n" + body
}

// ApplyPendingDeletes deletes the surviving side's note for every currently
// queued pending delete, then clears each one it successfully applied. A
// per-item failure is left queued for the next attempt rather than silently
// dropped.
func ApplyPendingDeletes(ctx context.Context, s *store.Store, vaultPath string) (applied int, errs []string, err error) {
	pending, err := s.ListMirrorPendingDeletes(ctx)
	if err != nil {
		return 0, nil, fmt.Errorf("list pending deletes: %w", err)
	}
	if len(pending) == 0 {
		return 0, nil, nil
	}

	obsidianNotes, lerr := notes.List(vaultPath)
	if lerr != nil {
		return 0, nil, fmt.Errorf("list vault: %w", lerr)
	}
	obsByID := indexByID(obsidianNotes)

	for _, p := range pending {
		var delErr error
		switch p.DeletedOn {
		case "apple":
			if old, ok := obsByID[p.ObsidianID]; ok {
				delErr = notes.Delete(vaultPath, old.Path)
			}
		case "obsidian":
			delErr = notes.DeleteApple(p.AppleID)
		}
		if delErr != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", p.Title, delErr))
			continue
		}
		if err := s.DeleteMirrorPendingDelete(ctx, p.ID); err != nil {
			errs = append(errs, fmt.Sprintf("%s: clearing queue entry: %v", p.Title, err))
			continue
		}
		applied++
	}
	return applied, errs, nil
}
```

- [ ] **Step 2: Build and run the existing tests**

Run: `go build ./... && go test ./internal/mirror/... -v`
Expected: build succeeds; the Task 2 tests still PASS (this task adds no new tests, only orchestration code).

- [ ] **Step 3: Commit**

```bash
git add internal/mirror/mirror.go
git commit -m "Add mirror.Sync orchestration and ApplyPendingDeletes"
```

---

## Task 4: Config flag and CLI wiring (`notectl sync` / `--apply-deletes`)

**Files:**
- Modify: `internal/config/config.go` (add `MirrorEnabled`, near `SyncSources` ~line 93)
- Modify: `cmd/sync.go`

**Interfaces:**
- Consumes: `mirror.Sync`, `mirror.ApplyPendingDeletes`, `mirror.Report` (Task 3); `config.SyncSources() []config.SourceType`, `config.SourceApple`, `config.SourceObsidian`, `config.SourceMarkdown`, `config.VaultPath() string` (existing).
- Produces: `config.MirrorEnabled() bool`; the `hasBothMirrorSources([]config.SourceType) bool` helper (package `cmd`, used again by Task 5's doctor check).

- [ ] **Step 1: Add `MirrorEnabled` to `internal/config/config.go`**

Insert right after the existing `SyncSources` function (after its closing `}`, ~line 93):

```go

// MirrorEnabled reports whether Apple Notes <-> Obsidian bidirectional
// mirror sync is turned on via mirror_apple_obsidian: true. It only takes
// effect when sync_sources also includes both apple and an obsidian-backed
// source — see cmd.hasBothMirrorSources and doctor's check for that.
func MirrorEnabled() bool {
	return viper.GetBool("mirror_apple_obsidian")
}
```

- [ ] **Step 2: Wire it into `cmd/sync.go`**

Replace the full file with:

```go
package cmd

import (
	"context"
	"fmt"

	"github.com/aeon022/notectl/internal/config"
	"github.com/aeon022/notectl/internal/mirror"
	"github.com/aeon022/notectl/internal/store"
	"github.com/aeon022/notectl/internal/syncdispatch"
	"github.com/spf13/cobra"
)

var applyMirrorDeletes bool

var syncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Sync notes from the configured source(s) into local cache",
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := store.New(config.DBPath(), config.Shared())
		if err != nil {
			return err
		}
		defer s.Close()
		ctx := context.Background()

		params := syncdispatch.ParamsFromConfig()
		var lastErr error
		for _, src := range config.SyncSources() {
			fmt.Print(syncLabel(src, params))
			ns, err := syncdispatch.List(src, params)
			if err != nil {
				fmt.Printf(" — failed: %v\n", err)
				lastErr = err
				continue
			}
			_ = s.DeleteBySource(ctx, syncdispatch.SourceKey(src))
			for i := range ns {
				_ = s.Upsert(ctx, &ns[i])
			}
			if byAccount, ferr := syncdispatch.SyncFolders(src); ferr == nil && byAccount != nil {
				_ = s.ReplaceFolders(ctx, syncdispatch.SourceKey(src), byAccount)
			}
			fmt.Printf("\n  %d notes indexed\n", len(ns))
		}

		if config.MirrorEnabled() {
			if !hasBothMirrorSources(config.SyncSources()) {
				fmt.Println("\nmirror_apple_obsidian is set, but sync_sources doesn't include both apple and obsidian — skipping mirror sync.")
			} else {
				report, mErr := mirror.Sync(ctx, s, params.VaultPath)
				fmt.Printf("\nMirror sync: %d created, %d updated, %d already linked, %d pending delete(s)\n",
					report.Created, report.Updated, report.LinkedExisting, report.PendingDeletes)
				for _, e := range report.Errors {
					fmt.Println("  ! " + e)
				}
				if report.PendingDeletes > 0 {
					fmt.Println("  Run 'notectl sync --apply-deletes' to remove the mirrored copies.")
				}
				if mErr != nil {
					lastErr = mErr
				}
			}
		}

		if applyMirrorDeletes {
			applied, errs, aErr := mirror.ApplyPendingDeletes(ctx, s, config.VaultPath())
			fmt.Printf("\nApplied %d pending mirror deletion(s)\n", applied)
			for _, e := range errs {
				fmt.Println("  ! " + e)
			}
			if aErr != nil {
				lastErr = aErr
			}
		}

		return lastErr
	},
}

func syncLabel(src config.SourceType, p syncdispatch.Params) string {
	switch src {
	case config.SourceApple:
		if p.AppleFolder != "" {
			return fmt.Sprintf("Syncing Apple Notes (folder: %s)", p.AppleFolder)
		}
		return "Syncing Apple Notes"
	case config.SourceJoplin:
		if p.JoplinFolder != "" {
			return fmt.Sprintf("Syncing Joplin (notebook: %s)", p.JoplinFolder)
		}
		return "Syncing Joplin"
	default:
		return fmt.Sprintf("Syncing vault: %s", p.VaultPath)
	}
}

// hasBothMirrorSources reports whether sources covers both an Apple Notes
// source and an obsidian-backed source (obsidian or markdown — they share
// one backend and both tag their cache rows "obsidian", see
// syncdispatch.SourceKey) — the minimum mirror.Sync needs to have anything
// to diff against on both sides.
func hasBothMirrorSources(sources []config.SourceType) bool {
	var hasApple, hasObsidian bool
	for _, s := range sources {
		switch s {
		case config.SourceApple:
			hasApple = true
		case config.SourceObsidian, config.SourceMarkdown:
			hasObsidian = true
		}
	}
	return hasApple && hasObsidian
}

func init() {
	syncCmd.Flags().BoolVar(&applyMirrorDeletes, "apply-deletes", false, "Apply queued mirror deletions (see mirror_apple_obsidian)")
	rootCmd.AddCommand(syncCmd)
}
```

- [ ] **Step 3: Build**

Run: `go build ./...`
Expected: succeeds with no errors.

- [ ] **Step 4: Manual smoke test against your real vault/account**

This is the point where "können wir testen" gets answered directly — do this with your real `~/.config/notectl/notectl.yaml` (already pointing at your Dropbox vault from the earlier session) and Apple Notes.

```bash
# add to ~/.config/notectl/notectl.yaml:
#   mirror_apple_obsidian: true
go run . sync
```

Expected: after the normal per-source sync output, a `Mirror sync: ...` line appears. On the very first run with existing notes on both sides, expect mostly `created` (or `already linked` for any exact folder+title matches) — check your vault and Apple Notes afterward to confirm no duplicates and no unexpected overwrites. Then edit one note on either side, run `go run . sync` again, and confirm the edit propagated to the other side.

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go cmd/sync.go
git commit -m "Wire mirror sync and --apply-deletes into notectl sync"
```

---

## Task 5: Wire into MCP `sync_notes` and TUI `s` key, add doctor checks

**Files:**
- Modify: `internal/mcpserver/server.go` (`handleSync`, ~line 447)
- Modify: `internal/tui/tui.go` (`doSyncCmd` ~line 3088, `syncDoneMsg` ~line 144, its handler ~line 602)
- Modify: `cmd/doctor.go`

**Interfaces:**
- Consumes: `mirror.Sync`, `mirror.Report` (Task 3); `hasBothMirrorSources` (Task 4, same `cmd` package for doctor.go); `store.CountMirrorPendingDeletes` (Task 1).

- [ ] **Step 1: Wire into `internal/mcpserver/server.go`'s `handleSync`**

Find the `handleSync` function and replace its body (from `summary := strings.Join(...)` to the end) — i.e. right before the final `return mcp.NewToolResultText(...)` — with mirror wiring. The full updated function:

```go
func handleSync(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	s, err := store.New(config.DBPath(), config.Shared())
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer s.Close()
	ctx := context.Background()

	params := syncdispatch.ParamsFromConfig()
	var results []string
	var lastErr error
	for _, src := range config.SyncSources() {
		srcKey := syncdispatch.SourceKey(src)
		ns, err := syncdispatch.List(src, params)
		if err != nil {
			results = append(results, fmt.Sprintf("%s: failed (%v)", srcKey, err))
			lastErr = err
			continue
		}
		_ = s.DeleteBySource(ctx, srcKey)
		for i := range ns {
			_ = s.Upsert(ctx, &ns[i])
		}
		if byAccount, ferr := syncdispatch.SyncFolders(src); ferr == nil && byAccount != nil {
			_ = s.ReplaceFolders(ctx, srcKey, byAccount)
		}
		results = append(results, fmt.Sprintf("%s: %d notes", srcKey, len(ns)))
	}

	if config.MirrorEnabled() {
		if report, mErr := mirror.Sync(ctx, s, params.VaultPath); mErr != nil {
			results = append(results, fmt.Sprintf("mirror: failed (%v)", mErr))
		} else {
			results = append(results, fmt.Sprintf("mirror: %d created, %d updated, %d pending delete(s)",
				report.Created, report.Updated, report.PendingDeletes))
			if report.PendingDeletes > 0 {
				results = append(results, "run 'notectl sync --apply-deletes' from the CLI to remove the mirrored copies")
			}
		}
	}

	summary := strings.Join(results, ", ")
	if lastErr != nil && len(results) == 1 {
		return mcp.NewToolResultError(lastErr.Error()), nil
	}
	return mcp.NewToolResultText("Synced — " + summary), nil
}
```

Add `"github.com/aeon022/notectl/internal/mirror"` to this file's import block.

Note this deliberately never calls `mirror.ApplyPendingDeletes` — deletion confirmation stays a human-initiated CLI action (`notectl sync --apply-deletes`), never something an MCP tool call triggers on its own.

- [ ] **Step 2: Wire into `internal/tui/tui.go`'s `doSyncCmd`**

First, extend `syncDoneMsg` (~line 144) to carry the pending-delete count:

```go
type syncDoneMsg struct {
	count         int
	mirrorPending int
	err           error
}
```

Then replace `doSyncCmd` (~line 3088-3117) with:

```go
// doSyncCmd syncs every source configured via config.SyncSources() (just
// the active Source() by default). One source failing doesn't block the
// rest — its error is still surfaced, but sources that succeeded are kept.
func doSyncCmd() tea.Cmd {
	return func() tea.Msg {
		s, err := store.New(config.DBPath(), config.Shared())
		if err != nil {
			return syncDoneMsg{err: err}
		}
		defer s.Close()
		ctx := context.Background()

		params := syncdispatch.ParamsFromConfig()
		var total int
		var lastErr error
		for _, src := range config.SyncSources() {
			ns, err := syncdispatch.List(src, params)
			if err != nil {
				lastErr = err
				continue
			}
			_ = s.DeleteBySource(ctx, syncdispatch.SourceKey(src))
			for i := range ns {
				_ = s.Upsert(ctx, &ns[i])
			}
			if byAccount, ferr := syncdispatch.SyncFolders(src); ferr == nil && byAccount != nil {
				_ = s.ReplaceFolders(ctx, syncdispatch.SourceKey(src), byAccount)
			}
			total += len(ns)
		}

		mirrorPending := 0
		if config.MirrorEnabled() {
			if report, mErr := mirror.Sync(ctx, s, params.VaultPath); mErr != nil {
				lastErr = mErr
			} else {
				total += report.Created + report.Updated
				mirrorPending = report.PendingDeletes
			}
		}

		return syncDoneMsg{count: total, mirrorPending: mirrorPending, err: lastErr}
	}
}
```

Add `"github.com/aeon022/notectl/internal/mirror"` to this file's import block.

Then update the `syncDoneMsg` case in the `Update` method (~line 602-611) to surface the pending count:

```go
	case syncDoneMsg:
		m.syncing = false
		if msg.err != nil {
			m.err = msg.err
		} else {
			status := fmt.Sprintf("Synced %d notes", msg.count)
			if msg.mirrorPending > 0 {
				status += fmt.Sprintf(" (%d mirror deletion(s) pending — run 'notectl sync --apply-deletes')", msg.mirrorPending)
			}
			m.setStatus(status)
			m.lastSynced = time.Now()
			_ = lastsync.Save(config.LastSyncedPath(), m.lastSynced)
			return m, loadNotesCmd(m.effectiveAccount(), m.activeFolder(), m.activeAccount())
		}
```

- [ ] **Step 3: Add doctor checks in `cmd/doctor.go`**

Add `"context"` and `"github.com/aeon022/notectl/internal/store"` to the import block, then add a `mirror sync` branch after the existing `switch config.Source()` block (before `if !doctor.PrintReport(checks) {`):

```go
		if config.MirrorEnabled() {
			if !hasBothMirrorSources(config.SyncSources()) {
				checks = append(checks, doctor.Check{
					Label: "Mirror sync", OK: false,
					Detail: "mirror_apple_obsidian is set, but sync_sources must include both apple and obsidian",
				})
			} else {
				checks = append(checks, checkMirrorPendingDeletes())
			}
		}
```

And add the helper function near `checkVaultPath`:

```go
// checkMirrorPendingDeletes surfaces the queued-deletion count so it's
// visible from `doctor` without needing to re-run `sync` first — deletions
// are never auto-applied, so this can otherwise sit invisible indefinitely.
func checkMirrorPendingDeletes() doctor.Check {
	s, err := store.New(config.DBPath(), config.Shared())
	if err != nil {
		return doctor.Check{Label: "Mirror pending deletes", OK: false, Detail: fmt.Sprintf("could not open cache: %v", err)}
	}
	defer s.Close()
	n, err := s.CountMirrorPendingDeletes(context.Background())
	if err != nil {
		return doctor.Check{Label: "Mirror pending deletes", OK: false, Detail: fmt.Sprintf("query failed: %v", err)}
	}
	if n == 0 {
		return doctor.Check{Label: "Mirror pending deletes", OK: true, Detail: "none"}
	}
	return doctor.Check{Label: "Mirror pending deletes", OK: true, Detail: fmt.Sprintf("%d queued — run 'notectl sync --apply-deletes' to apply", n)}
}
```

- [ ] **Step 4: Build and run the full test suite**

Run: `go build ./... && go test ./... -v`
Expected: build succeeds, every test package passes (Task 1's store tests, Task 2's mirror tests, and all pre-existing tests unaffected).

- [ ] **Step 5: Manual smoke test — TUI and doctor**

```bash
go run . doctor          # expect a "Mirror sync" or "Mirror pending deletes" line
go run . tui              # or just `go run .`; press 's' to sync, confirm status line shows the synced count
```

- [ ] **Step 6: Commit**

```bash
git add internal/mcpserver/server.go internal/tui/tui.go cmd/doctor.go
git commit -m "Surface mirror sync in MCP sync_notes, TUI sync, and doctor"
```

---

## Task 6: Documentation

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Add the config key to the config table**

In the config options table (the row right after `sync_sources`, ~line 294), add:

```markdown
| `mirror_apple_obsidian` | `true`/`false` (default `false`) — bidirectional mirror between Apple Notes and the Obsidian vault. Requires `sync_sources` to include both `apple` and `obsidian`/`markdown`. See [Mirroring Apple Notes and Obsidian](#mirroring-apple-notes-and-obsidian). |
```

- [ ] **Step 2: Add a new subsection after "Syncing multiple sources at once"**

Insert this new `###` subsection right before the `## MCP — AI Integration` header:

```markdown
### Mirroring Apple Notes and Obsidian

`mirror_apple_obsidian: true` turns `sync_sources: apple,obsidian` from a
read-only combined view into a real bidirectional mirror: a note created or
edited on either side is propagated to the other the next time `sync` runs
(CLI `notectl sync`, TUI `s`, or the `sync_notes` MCP tool).

```yaml
source: apple
sync_sources: apple,obsidian
mirror_apple_obsidian: true
vault_path: ~/path/to/your/vault
```

- Only the Apple Notes **default account** is mirrored.
- Folders map 1:1 by path (`Projects/Foo` in one side ↔ `Projects/Foo` in the other).
- If a note changed on both sides since the last sync, the newer one (by
  modification time) wins and overwrites the other.
- **Deletions are never applied automatically.** If a mirrored note
  disappears from one side, `sync` reports it and queues it — run
  `notectl sync --apply-deletes` to actually remove the mirrored copy on
  the other side. `notectl doctor` shows how many are currently queued.
- Tags don't mirror to Apple Notes (Apple Notes has no equivalent concept
  here) — an Obsidian note's tags are preserved in the vault but not pushed
  to its Apple Notes counterpart.
```

- [ ] **Step 3: Add the new flag to the Cheatsheet**

In the Cheatsheet code block (~line 39-47), change:

```
notectl sync                    index vault into SQLite
```

to:

```
notectl sync                    index vault into SQLite
notectl sync --apply-deletes    apply queued mirror deletions
```

- [ ] **Step 4: Commit**

```bash
git add README.md
git commit -m "Document mirror_apple_obsidian and --apply-deletes"
```
