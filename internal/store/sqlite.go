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

// migrate creates/upgrades the schema. `folders` is separate from `notes`
// deliberately: a folder only ever gets a row in `notes` by way of a note
// sitting inside it, so a real, currently-empty Apple Notes folder (e.g. a
// second account's own never-used "Notizen") has nowhere to be recorded at
// all otherwise — see ReplaceFolders/ListFolderInfo below and
// notes.ListAppleAccountFolders, which is what actually discovers them.
func (s *Store) migrate() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS notes (
			id        TEXT PRIMARY KEY,
			title     TEXT NOT NULL DEFAULT '',
			body      TEXT NOT NULL DEFAULT '',
			tags      TEXT NOT NULL DEFAULT '',
			event_id  TEXT NOT NULL DEFAULT '',
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

		CREATE TABLE IF NOT EXISTS folders (
			source  TEXT NOT NULL,
			account TEXT NOT NULL DEFAULT '',
			folder  TEXT NOT NULL,
			PRIMARY KEY (source, account, folder)
		);

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
	`)
	if err != nil {
		return err
	}
	if err := s.addColumnIfMissing("notes", "account", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	return s.addColumnIfMissing("notes", "event_id", "TEXT NOT NULL DEFAULT ''")
}

// addColumnIfMissing ALTERs table to add column (with the given type/
// constraint clause) if it doesn't already exist — for existing databases
// created before that column was introduced. CREATE TABLE IF NOT EXISTS
// above only helps on a brand-new DB; a pre-existing one needs its own
// column added explicitly since sqlite has no ADD COLUMN IF NOT EXISTS.
func (s *Store) addColumnIfMissing(table, column, def string) error {
	rows, err := s.db.Query(fmt.Sprintf(`PRAGMA table_info(%s)`, table))
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return err
		}
		if name == column {
			return rows.Err()
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = s.db.Exec(fmt.Sprintf(`ALTER TABLE %s ADD COLUMN %s %s`, table, column, def))
	return err
}

func (s *Store) Upsert(ctx context.Context, n *models.Note) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO notes (id,title,body,tags,event_id,folder,account,path,source,mod_time,created,synced_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
			title=excluded.title, body=excluded.body,
			tags=excluded.tags, event_id=excluded.event_id, folder=excluded.folder, account=excluded.account,
			mod_time=excluded.mod_time, synced_at=excluded.synced_at
	`,
		n.ID, n.Title, n.Body,
		strings.Join(n.Tags, ","),
		n.EventID,
		n.Folder, n.Account, n.Path, n.Source,
		n.ModTime.UTC().Format(time.RFC3339),
		n.Created.UTC().Format(time.RFC3339),
		time.Now().UTC().Format(time.RFC3339),
	)
	return err
}

type Filter struct {
	Source  string
	Account string
	Folder  string
	Tag     string
	EventID string
	Query   string
	Limit   int
}

// excludeMirroredObsidianSQL hides the Obsidian side of any active mirror
// link from a combined (all-sources) view — see List's doc comment on why.
const excludeMirroredObsidianSQL = ` AND NOT (source='obsidian' AND id IN (SELECT obsidian_id FROM mirror_links))`

func (s *Store) List(ctx context.Context, f Filter) ([]models.Note, error) {
	q := `SELECT id,title,body,tags,event_id,folder,account,path,source,mod_time,created FROM notes WHERE 1=1`
	var args []any
	if f.Source != "" {
		q += ` AND source=?`
		args = append(args, f.Source)
	} else {
		// No source filter means "everything, combined" — with the mirror
		// active, a mirrored note is TWO rows here (its Apple row and its
		// Obsidian mirror row), so an unfiltered list would show every
		// mirrored note twice. Apple is the side notectl treats as
		// canonical for combined browsing; hide the Obsidian row of any
		// currently-linked pair. An explicit source=obsidian filter still
		// sees it — that's a deliberate look at the vault's real content,
		// not the combined view this dedup is for.
		q += excludeMirroredObsidianSQL
	}
	if f.Account != "" {
		q += ` AND account=?`
		args = append(args, f.Account)
	}
	if f.Folder != "" {
		// Self + descendants: a note directly in "Projects" or anywhere
		// under "Projects/…" both count as being in the "Projects" tab —
		// selecting a top-level notebook aggregates its sub-notebooks
		// rather than showing an empty list when it's a pure container.
		// Scoped by account too (when given): different accounts can have
		// identically-named top-level folders (e.g. two "Notizen"), and
		// without the account filter this would silently mix them.
		q += ` AND (folder=? OR folder LIKE ? ESCAPE '\')`
		args = append(args, f.Folder, likeEscape(f.Folder)+`/%`)
	}
	if f.Tag != "" {
		// tags is stored comma-joined with no spaces (see Upsert); wrap both
		// sides in commas so this matches a whole tag, not a substring of a
		// longer one (e.g. "go" shouldn't match "golang"). LIKE is
		// case-insensitive for ASCII in SQLite by default, giving the
		// case-insensitive exact match this filter wants.
		q += ` AND (',' || tags || ',') LIKE ? ESCAPE '\'`
		args = append(args, "%,"+likeEscape(f.Tag)+",%")
	}
	if f.EventID != "" {
		// Unlike Tag, an event id is an opaque identifier from calctl, not a
		// human-typed word — exact, case-sensitive match, no LIKE needed.
		q += ` AND event_id=?`
		args = append(args, f.EventID)
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
		`SELECT id,title,body,tags,event_id,folder,account,path,source,mod_time,created FROM notes WHERE title=? LIMIT 1`,
		title)
	var n models.Note
	var tagsStr, modStr, createdStr string
	err := row.Scan(&n.ID, &n.Title, &n.Body, &tagsStr, &n.EventID, &n.Folder, &n.Account, &n.Path, &n.Source, &modStr, &createdStr)
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

// FindByTitle returns every note whose title exactly matches, most-recently
// modified first — GetByTitle's LIMIT 1 silently picks an arbitrary one when
// duplicate titles exist (a real occurrence here: Dropbox-conflict duplicate
// notes from concurrent multi-machine writes), which is tolerable for a read
// but not safe for an irreversible delete. Callers that need one unambiguous
// target should require len==1, disambiguating by folder if the caller has
// one, rather than guessing which duplicate the caller meant.
func (s *Store) FindByTitle(ctx context.Context, title string) ([]models.Note, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id,title,body,tags,event_id,folder,account,path,source,mod_time,created FROM notes WHERE title=? ORDER BY mod_time DESC`,
		title)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scan(rows)
}

func (s *Store) DeleteBySource(ctx context.Context, source string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM notes WHERE source=?`, source)
	return err
}

func (s *Store) Delete(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM notes WHERE id=?`, id)
	return err
}

// CountByFolder returns, for every folder path (plus "" for the grand
// total), the number of notes in it AND in any of its subfolders — a note
// in "Projects/Git" is counted under "Projects/Git", "Projects", and "".
// That rollup is what lets a top-level notebook tab show an aggregate count
// even when it's a pure container with no notes directly inside it,
// matching List's self+descendants folder filter above.
// CountByFolder returns per-folder note counts, optionally scoped to one
// account (empty account = every account, unscoped — the "All accounts"
// tab). Scoping matters here for the same reason it does everywhere else
// in this file: two accounts can have identically-named folders, and an
// unscoped GROUP BY folder would silently merge their counts.
func (s *Store) CountByFolder(ctx context.Context, account string) (map[string]int, error) {
	q := `SELECT folder, COUNT(*) FROM notes WHERE 1=1` + excludeMirroredObsidianSQL
	var args []any
	if account != "" {
		q += ` AND account = ?`
		args = append(args, account)
	}
	q += ` GROUP BY folder`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	total := 0
	counts := map[string]int{}
	for rows.Next() {
		var folder string
		var c int
		if err := rows.Scan(&folder, &c); err != nil {
			return nil, err
		}
		total += c
		if folder == "" {
			continue
		}
		segs := strings.Split(folder, "/")
		for i := range segs {
			counts[strings.Join(segs[:i+1], "/")] += c
		}
	}
	counts[""] = total
	return counts, rows.Err()
}

// likeEscape escapes the LIKE wildcard characters ('%', '_') and the
// backslash escape character itself, so a folder name containing them is
// matched literally rather than as a pattern.
func likeEscape(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return s
}

// ListAccounts returns every distinct non-empty Apple Notes account name
// present in the cache, sorted. Empty for obsidian-sourced notes, which
// have no account concept — callers should treat a nil/empty result as
// "no account row to show", not an error.
func (s *Store) ListAccounts(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT DISTINCT account FROM notes WHERE account != '' ORDER BY account`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var accounts []string
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			return nil, err
		}
		accounts = append(accounts, a)
	}
	return accounts, rows.Err()
}

// CountByAccount returns the note count per account (no rollup needed —
// unlike folders, accounts aren't nested), plus the grand total under ""
// for the "All accounts" row-0 tab — same convention as CountByFolder.
func (s *Store) CountByAccount(ctx context.Context) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT account, COUNT(*) FROM notes WHERE 1=1`+excludeMirroredObsidianSQL+` GROUP BY account`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	total := 0
	counts := map[string]int{}
	for rows.Next() {
		var account string
		var c int
		if err := rows.Scan(&account, &c); err != nil {
			return nil, err
		}
		total += c
		if account == "" {
			continue
		}
		counts[account] = c
	}
	counts[""] = total
	return counts, rows.Err()
}

// ReplaceFolders replaces every folders-table row for source with
// byAccount (account -> that account's own folder paths, as returned by
// e.g. notes.ListAppleAccountFolders) — delete-then-reinsert per sync, same
// convention DeleteBySource+Upsert already use for notes, so a folder
// removed since the last sync (deleted in Notes.app) doesn't linger as a
// phantom empty tab forever.
func (s *Store) ReplaceFolders(ctx context.Context, source string, byAccount map[string][]string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM folders WHERE source=?`, source); err != nil {
		return err
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT OR IGNORE INTO folders (source, account, folder) VALUES (?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for account, folders := range byAccount {
		for _, f := range folders {
			if f == "" {
				continue
			}
			if _, err := stmt.ExecContext(ctx, source, account, f); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// knownFolders returns every folder path in the folders table, optionally
// scoped to one account (empty = unscoped, every account+source). This is
// the "exists but maybe has nothing in it" half of FolderInfo's union — see
// folderInfo below.
func (s *Store) knownFolders(ctx context.Context, account string) ([]string, error) {
	q := `SELECT DISTINCT folder FROM folders WHERE folder != ''`
	var args []any
	if account != "" {
		q += ` AND account = ?`
		args = append(args, account)
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
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

// FolderInfo describes one (account, folder) pair known to notectl, with
// Count being its self+descendants note rollup (same convention
// CountByFolder uses). Count == 0 means the folder is real — synced from a
// live Apple Notes folder listing — but currently has nothing in it, which
// used to have no representation anywhere in the cache at all (see
// migrate's doc comment on the folders table).
type FolderInfo struct {
	Account string
	Folder  string
	Count   int
}

// folderInfo is the shared implementation behind ListFolderInfo (account
// == "", unscoped across everyone) and ListFolderInfoByAccount (scoped) —
// the note-derived, count-bearing folders (via CountByFolder's own
// self+descendants rollup) unioned with the folders table's possibly-empty
// ones, zero-filling any folder that only exists in the latter.
func (s *Store) folderInfo(ctx context.Context, account string) ([]FolderInfo, error) {
	counts, err := s.CountByFolder(ctx, account)
	if err != nil {
		return nil, err
	}
	known, err := s.knownFolders(ctx, account)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []FolderInfo
	for f, c := range counts {
		if f == "" || seen[f] {
			continue
		}
		seen[f] = true
		out = append(out, FolderInfo{Account: account, Folder: f, Count: c})
	}
	for _, f := range known {
		if seen[f] {
			continue
		}
		seen[f] = true
		out = append(out, FolderInfo{Account: account, Folder: f, Count: 0})
	}
	return out, nil
}

// ListFolderInfo is the "All accounts" row's data source: every folder
// known across every account+source, unscoped, each with its own note
// count (0 for a real-but-empty one).
func (s *Store) ListFolderInfo(ctx context.Context) ([]FolderInfo, error) {
	return s.folderInfo(ctx, "")
}

// ListFolderInfoByAccount is ListFolderInfo scoped to one account — used to
// detect a same-named top-level folder that collides across more than one
// Apple Notes account (see buildAccountAwareFolderTree in the tui package),
// which the unscoped ListFolderInfo can't tell apart on its own, for the
// same reason CountByFolder needs its own account-scoped parameter.
func (s *Store) ListFolderInfoByAccount(ctx context.Context, account string) ([]FolderInfo, error) {
	return s.folderInfo(ctx, account)
}

func scan(rows *sql.Rows) ([]models.Note, error) {
	var notes []models.Note
	for rows.Next() {
		var n models.Note
		var tagsStr, modStr, createdStr string
		if err := rows.Scan(
			&n.ID, &n.Title, &n.Body, &tagsStr, &n.EventID,
			&n.Folder, &n.Account, &n.Path, &n.Source, &modStr, &createdStr,
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
