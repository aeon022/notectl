package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/aeon022/missionctl-core/syncdir"
	"github.com/aeon022/notectl/internal/models"
	_ "modernc.org/sqlite"
)

type Store struct {
	db   *sql.DB
	path string
}

// notectl opens a fresh *Store per operation rather than holding one open
// for the process's lifetime, and flock(2) isn't reentrant within a
// process — locks reference-counts the real OS-level lock per path so the
// same process's own concurrent/sequential opens don't conflict with
// themselves; only the first open of a path acquires it for real, and only
// the last matching Close() releases it. A conflict is reported only when
// a genuinely different process holds it.
var (
	lockMu sync.Mutex
	locks  = map[string]*lockEntry{}
)

type lockEntry struct {
	lock  *syncdir.Lock
	count int
}

func acquireLock(path string) error {
	lockMu.Lock()
	defer lockMu.Unlock()
	e, ok := locks[path]
	if !ok {
		l, err := syncdir.Acquire(path)
		if err != nil {
			return err
		}
		e = &lockEntry{lock: l}
		locks[path] = e
	}
	e.count++
	return nil
}

func releaseLock(path string) {
	lockMu.Lock()
	defer lockMu.Unlock()
	e, ok := locks[path]
	if !ok {
		return
	}
	e.count--
	if e.count == 0 {
		e.lock.Release()
		delete(locks, path)
	}
}

// New opens the database at path. shared must reflect whether path is a
// user-configured (possibly folder-synced) directory rather than the
// tool's private default — see config.Shared.
func New(path string, shared bool) (*Store, error) {
	if isPlaceholder, placeholder := syncdir.ICloudPlaceholder(path); isPlaceholder {
		return nil, fmt.Errorf("%s hasn't finished downloading from iCloud yet (found %s) — open Finder and download it, or disable \"Optimize Mac Storage\" for this folder", path, placeholder)
	}

	if err := acquireLock(path); err != nil {
		if errors.Is(err, syncdir.ErrLocked) {
			return nil, fmt.Errorf("notectl is already running elsewhere, or a previous session crashed — remove %s.lock if you're sure nothing else is using it", path)
		}
		return nil, err
	}

	db, err := sql.Open("sqlite", path+"?_journal="+syncdir.JournalMode(shared)+"&_timeout=5000")
	if err != nil {
		releaseLock(path)
		return nil, fmt.Errorf("open db: %w", err)
	}
	s := &Store{db: db, path: path}
	if err := s.migrate(); err != nil {
		db.Close()
		releaseLock(path)
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error {
	err := s.db.Close()
	releaseLock(s.path)
	return err
}

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS notes (
			id        TEXT PRIMARY KEY,
			title     TEXT NOT NULL DEFAULT '',
			body      TEXT NOT NULL DEFAULT '',
			tags      TEXT NOT NULL DEFAULT '',
			folder    TEXT NOT NULL DEFAULT '',
			path      TEXT NOT NULL DEFAULT '',
			source    TEXT NOT NULL DEFAULT 'obsidian',
			mod_time  TEXT NOT NULL,
			created   TEXT NOT NULL,
			synced_at TEXT NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_notes_title   ON notes(title);
		CREATE INDEX IF NOT EXISTS idx_notes_source  ON notes(source);
		CREATE INDEX IF NOT EXISTS idx_notes_folder  ON notes(folder);
		CREATE INDEX IF NOT EXISTS idx_notes_modtime ON notes(mod_time);
	`)
	return err
}

func (s *Store) Upsert(ctx context.Context, n *models.Note) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO notes (id,title,body,tags,folder,path,source,mod_time,created,synced_at)
		VALUES (?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
			title=excluded.title, body=excluded.body,
			tags=excluded.tags, folder=excluded.folder,
			mod_time=excluded.mod_time, synced_at=excluded.synced_at
	`,
		n.ID, n.Title, n.Body,
		strings.Join(n.Tags, ","),
		n.Folder, n.Path, n.Source,
		n.ModTime.UTC().Format(time.RFC3339),
		n.Created.UTC().Format(time.RFC3339),
		time.Now().UTC().Format(time.RFC3339),
	)
	return err
}

type Filter struct {
	Source string
	Folder string
	Query  string
	Limit  int
}

func (s *Store) List(ctx context.Context, f Filter) ([]models.Note, error) {
	q := `SELECT id,title,body,tags,folder,path,source,mod_time,created FROM notes WHERE 1=1`
	var args []any
	if f.Source != "" {
		q += ` AND source=?`
		args = append(args, f.Source)
	}
	if f.Folder != "" {
		q += ` AND folder=?`
		args = append(args, f.Folder)
	}
	if f.Query != "" {
		q += ` AND (title LIKE ? OR body LIKE ? OR tags LIKE ?)`
		like := "%" + f.Query + "%"
		args = append(args, like, like, like)
	}
	q += ` ORDER BY mod_time DESC`
	if f.Limit > 0 {
		q += fmt.Sprintf(` LIMIT %d`, f.Limit)
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scan(rows)
}

func (s *Store) GetByTitle(ctx context.Context, title string) (*models.Note, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id,title,body,tags,folder,path,source,mod_time,created FROM notes WHERE title=? LIMIT 1`,
		title)
	var n models.Note
	var tagsStr, modStr, createdStr string
	err := row.Scan(&n.ID, &n.Title, &n.Body, &tagsStr, &n.Folder, &n.Path, &n.Source, &modStr, &createdStr)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	n.ModTime, _ = time.Parse(time.RFC3339, modStr)
	n.Created, _ = time.Parse(time.RFC3339, createdStr)
	if tagsStr != "" {
		n.Tags = strings.Split(tagsStr, ",")
	}
	return &n, nil
}

func (s *Store) DeleteBySource(ctx context.Context, source string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM notes WHERE source=?`, source)
	return err
}

func (s *Store) Delete(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM notes WHERE id=?`, id)
	return err
}

func (s *Store) CountByFolder(ctx context.Context) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT folder, COUNT(*) FROM notes GROUP BY folder`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	counts := map[string]int{}
	total := 0
	for rows.Next() {
		var folder string
		var c int
		if err := rows.Scan(&folder, &c); err != nil {
			return nil, err
		}
		counts[folder] = c
		total += c
	}
	counts[""] = total
	return counts, rows.Err()
}

func (s *Store) ListFolders(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT DISTINCT folder FROM notes WHERE folder != '' ORDER BY folder`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var folders []string
	for rows.Next() {
		var f string
		if err := rows.Scan(&f); err != nil {
			return nil, err
		}
		folders = append(folders, f)
	}
	return folders, rows.Err()
}

func scan(rows *sql.Rows) ([]models.Note, error) {
	var notes []models.Note
	for rows.Next() {
		var n models.Note
		var tagsStr, modStr, createdStr string
		if err := rows.Scan(
			&n.ID, &n.Title, &n.Body, &tagsStr,
			&n.Folder, &n.Path, &n.Source, &modStr, &createdStr,
		); err != nil {
			return nil, err
		}
		n.ModTime, _ = time.Parse(time.RFC3339, modStr)
		n.Created, _ = time.Parse(time.RFC3339, createdStr)
		if tagsStr != "" {
			n.Tags = strings.Split(tagsStr, ",")
		}
		notes = append(notes, n)
	}
	return notes, rows.Err()
}
