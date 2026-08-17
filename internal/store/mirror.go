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
