package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/aeon022/notectl/internal/models"
	_ "modernc.org/sqlite"
)

type Store struct{ db *sql.DB }

func New(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_journal=WAL&_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	s := &Store{db: db}
	return s, s.migrate()
}

func (s *Store) Close() error { return s.db.Close() }

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
