package mirror

import (
	"errors"
	"testing"
)

// TestStripLeadingTitleHeading guards the title-duplication bug: an
// Obsidian vault body always leads with "# title" (see notes.Write's
// buildContent), and Apple Notes gets its title prefixed separately
// (WriteApple/appleBody) — without stripping the vault's own heading first,
// every note pushed from Obsidian to Apple showed its title twice.
func TestStripLeadingTitleHeading(t *testing.T) {
	title := "Groceries"

	got := stripLeadingTitleHeading(title, "# Groceries\n\nmilk\neggs")
	if want := "milk\neggs"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}

	// No matching heading (title changed, or pre-fix vault file) — leave
	// the body untouched rather than guess.
	unchanged := "some other body"
	if got := stripLeadingTitleHeading(title, unchanged); got != unchanged {
		t.Errorf("got %q, want body left untouched: %q", got, unchanged)
	}
}

func TestPushToAppleNeverOverwritesAChecklistNote(t *testing.T) {
	var written []string
	write := func(id, html string) error { written = append(written, id); return nil }

	// native checklist → untouched, no error
	if wrote, err := pushToApple("n1", "<div>x</div>", func(string) (map[string]bool, error) { return map[string]bool{"Milch": true}, nil }, write); wrote || err != nil || len(written) != 0 {
		t.Fatalf("checklist note must be left alone: wrote=%v err=%v written=%v", wrote, err, written)
	}
	// state unreadable → refuse as well
	if wrote, err := pushToApple("n2", "<div>x</div>", func(string) (map[string]bool, error) { return nil, errors.New("db locked") }, write); wrote || err == nil || len(written) != 0 {
		t.Fatalf("unknown state must not write: wrote=%v err=%v", wrote, err)
	}
	// a plain note is written
	if wrote, err := pushToApple("n3", "<div>x</div>", func(string) (map[string]bool, error) { return map[string]bool{}, nil }, write); !wrote || err != nil || len(written) != 1 || written[0] != "n3" {
		t.Fatalf("plain note must be written: wrote=%v err=%v written=%v", wrote, err, written)
	}
}

func TestObsidianBodyKeepsCheckboxesOrSkipsTheNote(t *testing.T) {
	st := func(string) (map[string]bool, error) { return map[string]bool{"Milch": false}, nil }
	if got, err := obsidianBody("a", "Einkauf\n• Milch\n• Brot", st); err != nil || got != "Einkauf\n- [ ] Milch\n• Brot" {
		t.Fatalf("got %q, %v", got, err)
	}
	bad := func(string) (map[string]bool, error) { return nil, errors.New("no access") }
	if _, err := obsidianBody("a", "• Milch", bad); err == nil {
		t.Fatal("a note with list items and unreadable state must be skipped, never copied as bullets")
	}
	if got, err := obsidianBody("a", "nur Text", bad); err != nil || got != "nur Text" {
		t.Fatalf("a note without list items needs no state: %q, %v", got, err)
	}
}
