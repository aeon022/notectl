package notes

import (
	"testing"
	"unicode/utf16"

	"google.golang.org/protobuf/encoding/protowire"
)

// buildChecklistEntry encodes a Checklist message: {uuid: bytes=1, done: int32=2}.
func buildChecklistEntry(uuid byte, done int32) []byte {
	var b []byte
	b = protowire.AppendTag(b, 1, protowire.BytesType)
	b = protowire.AppendBytes(b, []byte{uuid})
	b = protowire.AppendTag(b, 2, protowire.VarintType)
	b = protowire.AppendVarint(b, uint64(done))
	return b
}

// buildParagraphStyle encodes a ParagraphStyle with only checklist (field 5) set.
func buildParagraphStyle(checklist []byte) []byte {
	var b []byte
	b = protowire.AppendTag(b, 5, protowire.BytesType)
	b = protowire.AppendBytes(b, checklist)
	return b
}

// buildAttributeRun encodes an AttributeRun {length: int32=1, paragraph_style: msg=2 (optional)}.
func buildAttributeRun(length int, paragraphStyle []byte) []byte {
	var b []byte
	b = protowire.AppendTag(b, 1, protowire.VarintType)
	b = protowire.AppendVarint(b, uint64(length))
	if paragraphStyle != nil {
		b = protowire.AppendTag(b, 2, protowire.BytesType)
		b = protowire.AppendBytes(b, paragraphStyle)
	}
	return b
}

// buildNote encodes a Note {note_text: string=2, attribute_run: repeated msg=5}.
func buildNote(noteText string, runs [][]byte) []byte {
	var b []byte
	b = protowire.AppendTag(b, 2, protowire.BytesType)
	b = protowire.AppendBytes(b, []byte(noteText))
	for _, r := range runs {
		b = protowire.AppendTag(b, 5, protowire.BytesType)
		b = protowire.AppendBytes(b, r)
	}
	return b
}

// buildDocument encodes a Document {note: msg=3}.
func buildDocument(note []byte) []byte {
	var b []byte
	b = protowire.AppendTag(b, 3, protowire.BytesType)
	b = protowire.AppendBytes(b, note)
	return b
}

// buildNoteStoreProto encodes a NoteStoreProto {document: msg=2}.
func buildNoteStoreProto(document []byte) []byte {
	var b []byte
	b = protowire.AppendTag(b, 2, protowire.BytesType)
	b = protowire.AppendBytes(b, document)
	return b
}

// utf16Len returns the UTF-16 code unit count of s, matching what
// AttributeRun.length counts (surrogate pairs for astral characters like
// most emoji count as 2, not 1).
func utf16Len(s string) int {
	return len(utf16.Encode([]rune(s)))
}

func TestParseChecklistState_BasicMixOfCheckedAndPlain(t *testing.T) {
	lines := []string{"Header", "Item A", "Item B", "Trailer"}
	text := lines[0] + "\n" + lines[1] + "\n" + lines[2] + "\n" + lines[3]

	runs := [][]byte{
		buildAttributeRun(utf16Len(lines[0]+"\n"), nil), // "Header\n" — plain, not a checklist line
		buildAttributeRun(utf16Len(lines[1]+"\n"), buildParagraphStyle(buildChecklistEntry(1, 0))), // "Item A\n" — unchecked
		buildAttributeRun(utf16Len(lines[2]+"\n"), buildParagraphStyle(buildChecklistEntry(2, 1))), // "Item B\n" — checked
		buildAttributeRun(utf16Len(lines[3]), nil), // "Trailer" — plain, no trailing newline
	}
	note := buildNote(text, runs)
	doc := buildDocument(note)
	blob := buildNoteStoreProto(doc)

	state, err := parseChecklistState(blob)
	if err != nil {
		t.Fatalf("parseChecklistState failed: %v", err)
	}

	if len(state) != 2 {
		t.Fatalf("expected exactly 2 checklist entries (Header/Trailer aren't checklist items), got %d: %v", len(state), state)
	}
	if done, ok := state["Item A"]; !ok || done {
		t.Errorf("Item A: got ok=%v done=%v, want ok=true done=false", ok, done)
	}
	if done, ok := state["Item B"]; !ok || !done {
		t.Errorf("Item B: got ok=%v done=%v, want ok=true done=true", ok, done)
	}
	if _, ok := state["Header"]; ok {
		t.Errorf("Header should not appear as a checklist entry")
	}
	if _, ok := state["Trailer"]; ok {
		t.Errorf("Trailer should not appear as a checklist entry")
	}
}

func TestParseChecklistState_EmojiUTF16Alignment(t *testing.T) {
	// Emoji are surrogate pairs in UTF-16 (2 code units, 1 Go rune) — this
	// specifically exercises that AttributeRun.length is UTF-16 code units,
	// not rune counts, across several emoji-leading lines in a row.
	lines := []string{"🎉 Party prep", "📋 Buy balloons", "🎂 Order cake", "Done section"}
	text := lines[0] + "\n" + lines[1] + "\n" + lines[2] + "\n" + lines[3]

	runs := [][]byte{
		buildAttributeRun(utf16Len(lines[0]+"\n"), nil),
		buildAttributeRun(utf16Len(lines[1]+"\n"), buildParagraphStyle(buildChecklistEntry(1, 0))),
		buildAttributeRun(utf16Len(lines[2]+"\n"), buildParagraphStyle(buildChecklistEntry(2, 1))),
		buildAttributeRun(utf16Len(lines[3]), nil),
	}
	note := buildNote(text, runs)
	doc := buildDocument(note)
	blob := buildNoteStoreProto(doc)

	state, err := parseChecklistState(blob)
	if err != nil {
		t.Fatalf("parseChecklistState failed: %v", err)
	}

	if done, ok := state["📋 Buy balloons"]; !ok || done {
		t.Errorf("📋 Buy balloons: got ok=%v done=%v, want ok=true done=false — misaligned UTF-16 offsets would corrupt this text or its neighbor", ok, done)
	}
	if done, ok := state["🎂 Order cake"]; !ok || !done {
		t.Errorf("🎂 Order cake: got ok=%v done=%v, want ok=true done=true", ok, done)
	}
	if len(state) != 2 {
		t.Fatalf("expected exactly 2 checklist entries, got %d: %v", len(state), state)
	}
}

func TestParseChecklistState_SplitRunsWithinOneChecklistLine(t *testing.T) {
	// A checklist paragraph can be split into multiple AttributeRuns (e.g.
	// part of the line is bold) — only one of them needs to carry the
	// checklist tag for the whole line to count.
	text := "bold and plain\n"
	boldPart := "bold and "
	plainPart := "plain\n"

	runs := [][]byte{
		buildAttributeRun(utf16Len(boldPart), buildParagraphStyle(buildChecklistEntry(1, 1))),
		buildAttributeRun(utf16Len(plainPart), nil),
	}
	note := buildNote(text, runs)
	doc := buildDocument(note)
	blob := buildNoteStoreProto(doc)

	state, err := parseChecklistState(blob)
	if err != nil {
		t.Fatalf("parseChecklistState failed: %v", err)
	}
	if done, ok := state["bold and plain"]; !ok || !done {
		t.Errorf("got ok=%v done=%v, want ok=true done=true", ok, done)
	}
}

func TestNormalizeChecklistText_MatchesStripHTMLEntityDecoding(t *testing.T) {
	// Apple's protobuf note_text keeps HTML-entity text literally (observed
	// on a real note: "Hebamme suchen &amp fixieren", no trailing semicolon)
	// while StripHTML's HTML-derived output already decodes it to "&". If
	// these two don't normalize to the same string, checklistLookup's map
	// lookup silently misses and the item renders as a plain bullet instead
	// of a checkbox — exactly the bug this guards against.
	sqliteText := "Hebamme suchen &amp fixieren (in Graz)"
	htmlDerivedText := "Hebamme suchen & fixieren (in Graz)"

	got := normalizeChecklistText(sqliteText)
	if got != htmlDerivedText {
		t.Errorf("normalizeChecklistText(%q) = %q, want %q (must match StripHTML's decoding)", sqliteText, got, htmlDerivedText)
	}
}

func TestNotePrimaryKey(t *testing.T) {
	tests := []struct {
		id      string
		want    int64
		wantErr bool
	}{
		{"apple-x-coredata://7AB17F46-BBD2-4F69-8FA7-96AE77B12D39/ICNote/p175", 175, false},
		{"x-coredata://7AB17F46-BBD2-4F69-8FA7-96AE77B12D39/ICNote/p1", 1, false},
		{"not-an-apple-id", 0, true},
	}
	for _, tc := range tests {
		got, err := notePrimaryKey(tc.id)
		if tc.wantErr {
			if err == nil {
				t.Errorf("notePrimaryKey(%q): expected error, got %d", tc.id, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("notePrimaryKey(%q): unexpected error: %v", tc.id, err)
		}
		if got != tc.want {
			t.Errorf("notePrimaryKey(%q) = %d, want %d", tc.id, got, tc.want)
		}
	}
}
