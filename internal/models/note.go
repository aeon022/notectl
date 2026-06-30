package models

import "time"

type Note struct {
	ID      string
	Title   string
	Body    string
	Tags    []string
	Folder  string
	Path    string    // relative path in vault (obsidian only)
	Source  string    // "obsidian" | "apple"
	ModTime time.Time
	Created time.Time
}
