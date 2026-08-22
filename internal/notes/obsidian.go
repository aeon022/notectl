package notes

import (
	"crypto/sha1"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aeon022/notectl/internal/models"
	"gopkg.in/yaml.v3"
)

// List returns all markdown notes in the vault, sorted by mod time
// descending. exclude names top-level-or-nested folders (matched against
// any path segment, same convention as the built-in hidden-dir skip below)
// that belong to another tool sharing this vault — e.g. a diaryctl "Diary"
// folder — and should never be treated as notectl's own notes.
func List(vaultPath string, exclude []string) ([]models.Note, error) {
	var notes []models.Note
	err := filepath.Walk(vaultPath, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".md") {
			return nil
		}
		rel, _ := filepath.Rel(vaultPath, path)
		for _, part := range strings.Split(filepath.Dir(rel), string(os.PathSeparator)) {
			// "." is the vault root, not a real directory segment.
			if part == "." {
				continue
			}
			// skip hidden dirs (.obsidian, .git, etc.)
			if strings.HasPrefix(part, ".") {
				return nil
			}
			for _, ex := range exclude {
				if part == ex {
					return nil
				}
			}
		}
		n, err := readFile(vaultPath, path, info)
		if err != nil {
			return nil // skip unreadable files
		}
		notes = append(notes, n)
		return nil
	})
	return notes, err
}

// Read returns a single note by title (case-insensitive match on filename).
func Read(vaultPath, title string) (*models.Note, error) {
	target := slugify(title) + ".md"
	var found *models.Note
	_ = filepath.Walk(vaultPath, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if strings.EqualFold(filepath.Base(path), target) ||
			strings.EqualFold(stripExt(filepath.Base(path)), title) {
			n, e := readFile(vaultPath, path, info)
			if e == nil {
				found = &n
				return filepath.SkipAll
			}
		}
		return nil
	})
	return found, nil
}

// TargetPath returns the vault-relative path Write would write this
// title/folder to. Write overwrites whatever sits there unconditionally, so
// a caller that must not clobber an unrelated note (two different titles can
// slugify to the same filename) can check this against the paths it already
// listed first.
func TargetPath(title, folder string) string {
	return filepath.Join(folder, slugify(title)+".md")
}

// Write creates or overwrites a note in the vault.
//
// Known limitation: buildContent only re-emits tags/event_id/created, so any
// other frontmatter key an existing file carries is dropped on overwrite.
func Write(vaultPath, title, body string, tags []string, folder, eventID string) (*models.Note, error) {
	dir := vaultPath
	if folder != "" {
		dir = filepath.Join(vaultPath, folder)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	filename := slugify(title) + ".md"
	fullPath := filepath.Join(dir, filename)

	content := buildContent(title, body, tags, eventID)
	if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
		return nil, err
	}
	info, _ := os.Stat(fullPath)
	n, _ := readFile(vaultPath, fullPath, info)
	return &n, nil
}

// Delete removes a note file from the vault.
func Delete(vaultPath, relPath string) error {
	full := filepath.Join(vaultPath, relPath)
	return os.Remove(full)
}

// Search does a fast grep-style search through note files.
func Search(vaultPath, query string, limit int) ([]models.Note, error) {
	q := strings.ToLower(query)
	var notes []models.Note
	_ = filepath.Walk(vaultPath, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}
		data, e := os.ReadFile(path)
		if e != nil {
			return nil
		}
		if strings.Contains(strings.ToLower(string(data)), q) ||
			strings.Contains(strings.ToLower(stripExt(filepath.Base(path))), q) {
			n, e := readFile(vaultPath, path, info)
			if e == nil {
				notes = append(notes, n)
			}
		}
		if limit > 0 && len(notes) >= limit {
			return filepath.SkipAll
		}
		return nil
	})
	return notes, nil
}

// ── Helpers ───────────────────────────────────────────────────────────────────

type frontmatter struct {
	Title   string    `yaml:"title"`
	Tags    []string  `yaml:"tags"`
	EventID string    `yaml:"event_id,omitempty"`
	Created time.Time `yaml:"created"`
}

func readFile(vaultPath, path string, info os.FileInfo) (models.Note, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return models.Note{}, err
	}
	rel, _ := filepath.Rel(vaultPath, path)
	folder := filepath.Dir(rel)
	if folder == "." {
		folder = ""
	}
	title := stripExt(filepath.Base(path))
	body := string(data)
	var fm frontmatter
	created := info.ModTime()

	// parse YAML frontmatter if present
	if strings.HasPrefix(body, "---\n") {
		if end := strings.Index(body[4:], "\n---"); end >= 0 {
			fmBlock := body[4 : 4+end]
			_ = yaml.Unmarshal([]byte(fmBlock), &fm)
			body = strings.TrimSpace(body[4+end+4:])
			if !fm.Created.IsZero() {
				created = fm.Created
			}
			// A slugified filename (spaces/emoji/punctuation stripped, see
			// slugify below) is lossy and can never round-trip back to the
			// real title on its own — without this, re-reading a note this
			// package itself wrote reports a mangled title that no longer
			// matches its Apple Notes counterpart, which broke mirror's
			// exact-title bootstrap matching (see internal/mirror/decide.go)
			// and spawned duplicate notes instead of linking to the existing
			// one. Notes without a title: frontmatter entry (pre-existing
			// vault files, or ones from outside notectl) keep using the
			// filename as before.
			if fm.Title != "" {
				title = fm.Title
			}
		}
	}

	id := noteID(rel)
	return models.Note{
		ID:      id,
		Title:   title,
		Body:    body,
		Tags:    fm.Tags,
		EventID: fm.EventID,
		Folder:  folder,
		Path:    rel,
		Source:  "obsidian",
		ModTime: info.ModTime(),
		Created: created,
	}, nil
}

func buildContent(title, body string, tags []string, eventID string) string {
	var sb strings.Builder
	sb.WriteString("---\ntitle: " + yamlSingleQuote(title) + "\n")
	if len(tags) > 0 {
		sb.WriteString("tags:")
		for _, t := range tags {
			sb.WriteString("\n  - " + t)
		}
		sb.WriteString("\n")
	}
	if eventID != "" {
		sb.WriteString("event_id: " + yamlSingleQuote(eventID) + "\n")
	}
	sb.WriteString("created: " + time.Now().Format("2006-01-02") + "\n---\n\n")
	sb.WriteString("# " + title + "\n\n")
	sb.WriteString(body)
	return sb.String()
}

// yamlSingleQuote renders s as a single-quoted YAML scalar (doubling
// embedded quotes, YAML's own escape for that style) so a title containing
// ":", "#", or other block-scalar-breaking characters can't corrupt the
// frontmatter block.
func yamlSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func noteID(rel string) string {
	h := sha1.Sum([]byte(rel))
	return fmt.Sprintf("%x", h[:8])
}

func slugify(s string) string {
	s = strings.TrimSpace(s)
	var out strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '-', r == '_', r == ' ':
			out.WriteRune(r)
		}
	}
	return strings.ReplaceAll(out.String(), " ", "-")
}

func stripExt(name string) string {
	if ext := filepath.Ext(name); ext != "" {
		return name[:len(name)-len(ext)]
	}
	return name
}
