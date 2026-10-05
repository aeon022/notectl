package notes

import (
	"strings"
	"testing"
	"time"
)

func TestParseAppleNotes(t *testing.T) {
	out := "---NOTE---\nID:x-coredata://1\nTITLE:Einkauf\nFOLDER:Privat\nACCOUNT:iCloud\nMODTIME:2026-10-05T09:30:00\nBODY:<div>Milch</div>\n<div>Brot</div>\n" +
		"---NOTE---\nID:x-coredata://2\nTITLE:  \nFOLDER:Notes\nBODY:\n" +
		"---NOTE---\nTITLE:ohne ID\nBODY:<div>x</div>\n"
	notes := parseAppleNotes(out)
	if len(notes) != 2 {
		t.Fatalf("got %d notes, want 2 (the one without ID is dropped): %+v", len(notes), notes)
	}
	a := notes[0]
	if a.ID != "apple-x-coredata://1" || a.Title != "Einkauf" || a.Folder != "Privat" || a.Account != "iCloud" || a.Source != "apple" {
		t.Errorf("note 0 = %+v", a)
	}
	if want := time.Date(2026, 10, 5, 9, 30, 0, 0, time.Local); !a.ModTime.Equal(want) {
		t.Errorf("ModTime = %v", a.ModTime)
	}
	if !strings.Contains(a.Body, "Milch") || !strings.Contains(a.Body, "Brot") {
		t.Errorf("multi-line body lost lines: %q", a.Body)
	}
	if notes[1].Title != "Untitled" {
		t.Errorf("blank title must become Untitled, got %q", notes[1].Title)
	}
}
