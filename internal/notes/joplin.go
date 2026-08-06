package notes

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/aeon022/notectl/internal/models"
)

// Joplin's plugin API can only run inside a live Joplin process, so a
// standalone tool like notectl instead talks to Joplin's Data API — the
// same local REST service (default http://localhost:41184, token-based)
// browser clippers and other external tools use. This means, unlike the
// Apple Notes and Obsidian sources, a Joplin source requires Joplin itself
// to be running with the Web Clipper service enabled (Options → Web
// Clipper). If it isn't reachable, every function here returns a clear
// error saying so rather than silently doing nothing.

type joplinNoteJSON struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	ParentID  string `json:"parent_id"`
	CreatedMs int64  `json:"created_time"`
	UpdatedMs int64  `json:"updated_time"`
}

type joplinFolderJSON struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	ParentID string `json:"parent_id"`
}

type joplinListResponse[T any] struct {
	Items   []T  `json:"items"`
	HasMore bool `json:"has_more"`
}

var joplinHTTPClient = &http.Client{Timeout: 8 * time.Second}

// joplinRequest performs a Data API call and decodes the JSON response into
// out (if non-nil). Connection failures are rewrapped into a message
// pointing at the actual fix (start Joplin, enable Web Clipper) instead of
// a raw "connection refused".
func joplinRequest(method, path string, query url.Values, body any, out any) error {
	base, token, err := joplinBaseAndToken()
	if err != nil {
		return err
	}

	if query == nil {
		query = url.Values{}
	}
	query.Set("token", token)

	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("joplin: encode request: %w", err)
		}
		bodyReader = bytes.NewReader(b)
	}

	fullURL := strings.TrimRight(base, "/") + path + "?" + query.Encode()
	req, err := http.NewRequest(method, fullURL, bodyReader)
	if err != nil {
		return fmt.Errorf("joplin: build request: %w", err)
	}
	if bodyReader != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := joplinHTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("joplin: not reachable at %s — start Joplin and enable Web Clipper (Options → Web Clipper): %w", base, err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("joplin: %s %s failed (%d): %s", method, path, resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	if out != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, out); err != nil {
			return fmt.Errorf("joplin: decode response: %w", err)
		}
	}
	return nil
}

func joplinBaseAndToken() (string, string, error) {
	base := joplinAPIURLFunc()
	token := joplinTokenFunc()
	if token == "" {
		return "", "", fmt.Errorf("joplin: no API token configured — set joplin_token in ~/.config/notectl/notectl.yaml or NOTECTL_JOPLIN_TOKEN (find it in Joplin under Options → Web Clipper)")
	}
	return base, token, nil
}

// joplinAPIURLFunc and joplinTokenFunc are indirected through config at call
// time via SetJoplinConfigFuncs — avoids an import cycle (config doesn't
// need to know about notes, notes doesn't need to know about viper).
var (
	joplinAPIURLFunc = func() string { return "http://localhost:41184" }
	joplinTokenFunc  = func() string { return "" }
)

// SetJoplinConfigFuncs wires this package's Joplin API URL/token lookups to
// the real config package. Called once from main/cmd init.
func SetJoplinConfigFuncs(apiURL func() string, token func() string) {
	joplinAPIURLFunc = apiURL
	joplinTokenFunc = token
}

func joplinToNote(j joplinNoteJSON, folderTitle string) models.Note {
	return models.Note{
		ID:      j.ID,
		Title:   j.Title,
		Body:    j.Body,
		Folder:  folderTitle,
		Source:  "joplin",
		ModTime: time.UnixMilli(j.UpdatedMs),
		Created: time.UnixMilli(j.CreatedMs),
	}
}

// ListJoplin returns all notes, optionally restricted to one folder (by
// title or Parent/Child nested path, matching JoplinBridge's convention).
func ListJoplin(folder string) ([]models.Note, error) {
	folders, err := fetchJoplinFolders()
	if err != nil {
		return nil, err
	}
	idToTitle := make(map[string]string, len(folders))
	for _, f := range folders {
		idToTitle[f.ID] = f.Title
	}

	var wantParentID string
	if folder != "" {
		wantParentID, err = resolveJoplinFolderPath(folders, folder, false)
		if err != nil {
			return nil, err
		}
	}

	var all []models.Note
	page := 1
	for {
		var resp joplinListResponse[joplinNoteJSON]
		q := url.Values{
			"fields":    {"id,title,body,parent_id,created_time,updated_time"},
			"limit":     {"100"},
			"page":      {fmt.Sprint(page)},
			"order_by":  {"updated_time"},
			"order_dir": {"DESC"},
		}
		if err := joplinRequest(http.MethodGet, "/notes", q, nil, &resp); err != nil {
			return nil, err
		}
		for _, j := range resp.Items {
			if wantParentID != "" && j.ParentID != wantParentID {
				continue
			}
			all = append(all, joplinToNote(j, idToTitle[j.ParentID]))
		}
		if !resp.HasMore {
			break
		}
		page++
	}
	return all, nil
}

// ReadJoplin fetches a single note by id.
func ReadJoplin(id string) (*models.Note, error) {
	var j joplinNoteJSON
	q := url.Values{"fields": {"id,title,body,parent_id,created_time,updated_time"}}
	if err := joplinRequest(http.MethodGet, "/notes/"+url.PathEscape(id), q, nil, &j); err != nil {
		return nil, err
	}

	folderTitle := ""
	if j.ParentID != "" {
		var f joplinFolderJSON
		if err := joplinRequest(http.MethodGet, "/folders/"+url.PathEscape(j.ParentID), url.Values{"fields": {"id,title"}}, nil, &f); err == nil {
			folderTitle = f.Title
		}
	}

	n := joplinToNote(j, folderTitle)
	return &n, nil
}

// WriteJoplin creates a new note (id == "") or updates an existing one's
// title and body (id != ""). Unlike WriteApple, update also touches title
// here: Apple Notes derives its displayed title from the body's first
// line, so setting title directly there was found to desync rather than
// rename it (see WriteApple's doc comment) — Joplin has no such quirk,
// title is a first-class field, so a rename here just works. Folder is not
// changed on update; move a note between notebooks in Joplin itself. On
// create, folder may be a single name or a Parent/Child nested path;
// missing folders are created automatically.
func WriteJoplin(id, title, body, folder string) (string, error) {
	if id != "" {
		payload := map[string]string{"title": title, "body": body}
		if err := joplinRequest(http.MethodPut, "/notes/"+url.PathEscape(id), nil, payload, nil); err != nil {
			return "", err
		}
		return id, nil
	}

	var parentID string
	if folder != "" {
		folders, err := fetchJoplinFolders()
		if err != nil {
			return "", err
		}
		parentID, err = resolveJoplinFolderPath(folders, folder, true)
		if err != nil {
			return "", err
		}
	}

	payload := map[string]string{"title": title, "body": body}
	if parentID != "" {
		payload["parent_id"] = parentID
	}
	var created joplinNoteJSON
	if err := joplinRequest(http.MethodPost, "/notes", nil, payload, &created); err != nil {
		return "", err
	}
	return created.ID, nil
}

// DeleteJoplin permanently deletes a note (query param matches the Data
// API's own flag — Joplin's own UI moves to trash first, but the Data API
// deletes outright unless told otherwise; kept consistent with how
// DeleteApple/DeleteInMail behave elsewhere in this suite: gone means
// gone). Unlike the AppleScript sources, a delete on a nonexistent id
// naturally returns a real 404 here — no bare-try-swallows-everything
// class of bug to worry about.
func DeleteJoplin(id string) error {
	return joplinRequest(http.MethodDelete, "/notes/"+url.PathEscape(id), nil, nil, nil)
}

// SearchJoplin does a live full-text search via Joplin's own search index.
func SearchJoplin(query string, limit int) ([]models.Note, error) {
	folders, err := fetchJoplinFolders()
	if err != nil {
		return nil, err
	}
	idToTitle := make(map[string]string, len(folders))
	for _, f := range folders {
		idToTitle[f.ID] = f.Title
	}

	var resp joplinListResponse[joplinNoteJSON]
	q := url.Values{
		"query":  {query},
		"type":   {"note"},
		"fields": {"id,title,body,parent_id,created_time,updated_time"},
		"limit":  {fmt.Sprint(limit)},
	}
	if err := joplinRequest(http.MethodGet, "/search", q, nil, &resp); err != nil {
		return nil, err
	}

	out := make([]models.Note, 0, len(resp.Items))
	for _, j := range resp.Items {
		out = append(out, joplinToNote(j, idToTitle[j.ParentID]))
	}
	return out, nil
}

// ListJoplinFolders returns every notebook's full nested path (e.g.
// "MISSIONCTL/Marketing"), matching JoplinBridge's own path convention.
func ListJoplinFolders() ([]string, error) {
	folders, err := fetchJoplinFolders()
	if err != nil {
		return nil, err
	}
	idToFolder := make(map[string]joplinFolderJSON, len(folders))
	for _, f := range folders {
		idToFolder[f.ID] = f
	}

	pathFor := func(id string) string {
		var parts []string
		currentID := id
		for guard := 0; currentID != "" && guard < 50; guard++ {
			f, ok := idToFolder[currentID]
			if !ok {
				break
			}
			parts = append([]string{f.Title}, parts...)
			currentID = f.ParentID
		}
		return strings.Join(parts, "/")
	}

	out := make([]string, 0, len(folders))
	for _, f := range folders {
		out = append(out, pathFor(f.ID))
	}
	return out, nil
}

func fetchJoplinFolders() ([]joplinFolderJSON, error) {
	var all []joplinFolderJSON
	page := 1
	for {
		var resp joplinListResponse[joplinFolderJSON]
		q := url.Values{"fields": {"id,title,parent_id"}, "limit": {"100"}, "page": {fmt.Sprint(page)}}
		if err := joplinRequest(http.MethodGet, "/folders", q, nil, &resp); err != nil {
			return nil, err
		}
		all = append(all, resp.Items...)
		if !resp.HasMore {
			break
		}
		page++
	}
	return all, nil
}

// resolveJoplinFolderPath walks a "Parent/Child" path one level at a time,
// same convention as JoplinBridge's ensureJoplinFolder. If create is false
// and a segment doesn't exist, returns an error instead of creating it —
// used for filtering an existing list, where a typo should be obvious
// rather than silently matching nothing.
func resolveJoplinFolderPath(folders []joplinFolderJSON, path string, create bool) (string, error) {
	byParentTitle := make(map[string]string, len(folders)) // "parentID:lower(title)" -> id
	for _, f := range folders {
		byParentTitle[f.ParentID+":"+strings.ToLower(f.Title)] = f.ID
	}

	var parentID string
	for _, part := range strings.Split(path, "/") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		key := parentID + ":" + strings.ToLower(part)
		if id, ok := byParentTitle[key]; ok {
			parentID = id
			continue
		}
		if !create {
			return "", fmt.Errorf("joplin: folder %q not found", path)
		}
		var created joplinFolderJSON
		payload := map[string]string{"title": part}
		if parentID != "" {
			payload["parent_id"] = parentID
		}
		if err := joplinRequest(http.MethodPost, "/folders", nil, payload, &created); err != nil {
			return "", err
		}
		parentID = created.ID
		byParentTitle[key] = parentID
	}
	return parentID, nil
}
