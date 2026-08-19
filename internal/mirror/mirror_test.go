package mirror

import "testing"

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
