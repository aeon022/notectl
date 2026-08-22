package models

import (
	"strings"
	"time"
)

type Note struct {
	ID      string
	Title   string
	Body    string
	Tags    []string
	Folder  string
	Account string // Apple Notes account name (apple only, "" for obsidian) — e.g. "iCloud", "FH Burgenland"
	Path    string // relative path in vault (obsidian only)
	Source  string // "obsidian" | "apple"
	ModTime time.Time
	Created time.Time
}

// HasTag reports whether tag exactly matches (case-insensitively) any entry
// in n.Tags — used by the live-file-search fallbacks that can't push tag
// filtering down into a SQL query (see store.Filter.Tag for the cached-list
// equivalent).
func (n Note) HasTag(tag string) bool {
	for _, t := range n.Tags {
		if strings.EqualFold(t, tag) {
			return true
		}
	}
	return false
}

// FilterByTag returns the subset of notes whose Tags contain tag (see
// HasTag). Used by callers that fell back to a live, non-SQL search (store.
// Filter.Tag handles the same filtering directly in SQL for the cached
// path).
func FilterByTag(notes []Note, tag string) []Note {
	var out []Note
	for _, n := range notes {
		if n.HasTag(tag) {
			out = append(out, n)
		}
	}
	return out
}
