package notes

import (
	"bytes"
	"compress/gzip"
	"database/sql"
	"fmt"
	"html"
	"io"
	"os"
	"strconv"
	"strings"
	"unicode/utf16"

	"google.golang.org/protobuf/encoding/protowire"
	_ "modernc.org/sqlite"
)

// Apple Notes' AppleScript dictionary does not expose whether a list item is
// a genuine checklist entry, nor its checked/unchecked state — `body of note`
// returns identical <ul><li> markup either way (confirmed by direct testing:
// see notectl's git history / commit message for this file). That state does
// exist locally, though, in Notes' own SQLite database, inside a gzip'd
// protobuf blob per note. This file reads it — read-only, best-effort, and
// never a hard dependency: every function here degrades to "no info" on any
// error (missing file, no Full Disk Access, schema drift on some future
// macOS) rather than failing the caller. Writing checked state back through
// this path is deliberately not implemented: Notes.app has this database
// open live and it also round-trips through iCloud sync, so mutating it
// out-of-band risks corruption or sync conflicts. Toggling a checkbox in
// notectl updates the plain-text ☐/☑ character in the note body (via the
// normal AppleScript write path in apple.go) but cannot flip Apple's native
// checkbox — only Notes.app itself can do that.
//
// Schema reverse-engineered by github.com/threeplanetssoftware/apple_cloud_notes_parser
// (proto/notestore.proto); only the small slice of it needed for checklist
// state (Note.note_text, Note.attribute_run[].paragraph_style.checklist.done)
// is reimplemented here via protowire, to avoid pulling in a full protoc
// codegen step for a handful of fields.

// ChecklistState returns, for an Apple note, a lookup from each checklist
// item's trimmed text to its real checked state — the ground truth Notes.app
// itself uses, unavailable anywhere else. Returns (nil, err) if the local
// Notes database can't be read for any reason; callers should treat that as
// "unknown" and fall back to their existing behavior, not as fatal.
func ChecklistState(appleID string) (map[string]bool, error) {
	pk, err := notePrimaryKey(appleID)
	if err != nil {
		return nil, err
	}

	data, err := readNoteBlob(pk)
	if err != nil {
		return nil, err
	}

	plain, err := gunzip(data)
	if err != nil {
		return nil, fmt.Errorf("decompress note blob: %w", err)
	}

	return parseChecklistState(plain)
}

// notePrimaryKey extracts the ZICCLOUDSYNCINGOBJECT.Z_PK from an Apple note
// ID of the form "apple-x-coredata://<uuid>/ICNote/p<pk>" — the same numeric
// key AppleScript's own note id encodes, so no separate lookup is needed.
func notePrimaryKey(appleID string) (int64, error) {
	id := rawAppleID(appleID)
	idx := strings.LastIndex(id, "/p")
	if idx < 0 {
		return 0, fmt.Errorf("unrecognized apple note id format: %q", appleID)
	}
	pk, err := strconv.ParseInt(id[idx+2:], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse note primary key from id %q: %w", appleID, err)
	}
	return pk, nil
}

func noteStorePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return home + "/Library/Group Containers/group.com.apple.notes/NoteStore.sqlite", nil
}

func readNoteBlob(pk int64) ([]byte, error) {
	path, err := noteStorePath()
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("Notes database not accessible (%w) — likely missing Full Disk Access", err)
	}

	// Read-only: this database is live-owned by Notes.app and synced by
	// CloudKit; notectl only ever reads it. Deliberately NOT using
	// ?immutable=1 here even though the connection is read-only — that flag
	// tells SQLite the file will never change for the life of the
	// connection, which is false: Notes.app writes to it continuously while
	// running, and immutable's caching caused genuinely stale reads (a note
	// edited seconds earlier read back as "no rows" until the process was
	// restarted) during testing.
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		return nil, err
	}
	defer db.Close()

	var data []byte
	err = db.QueryRow(`SELECT ZDATA FROM ZICNOTEDATA WHERE ZNOTE = ?`, pk).Scan(&data)
	if err != nil {
		return nil, fmt.Errorf("query note data for pk %d: %w", pk, err)
	}
	return data, nil
}

func gunzip(data []byte) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	return io.ReadAll(gz)
}

// paragraphStyleChecklist is ParagraphStyle.style_type's value for a
// paragraph Notes.app currently renders as a checklist item (checkbox
// visible). Determined empirically against a real note's data: its actual
// checklist items were all style_type 103, while a different note's plain
// "dashed list" bullets (style_type 100) sometimes still carried a leftover
// Checklist submessage from an earlier edit — style_type is what Notes.app
// itself keys off, not just whether a Checklist submessage is present.
const paragraphStyleChecklist = 103

// parseChecklistState walks the note's AttributeRun sequence in step with
// its note_text, in UTF-16 code units (Apple's NSString-backed length unit —
// using Go rune counts here would drift out of alignment on every emoji,
// which this note format is full of, since surrogate pairs count as one rune
// but two UTF-16 units). Each run covering any part of a paragraph tagged
// with a checklist marks that whole paragraph; the paragraph's done state
// wins even if only set on one of several runs within it (bold/color spans
// inside a checklist line each get their own run, but only need one to carry
// the checklist tag in practice).
func parseChecklistState(plain []byte) (map[string]bool, error) {
	doc, ok := getMessageField(plain, 2) // NoteStoreProto.document
	if !ok {
		return nil, fmt.Errorf("no document field in note protobuf")
	}
	note, ok := getMessageField(doc, 3) // Document.note
	if !ok {
		return nil, fmt.Errorf("no note field in document protobuf")
	}
	noteText, ok := getMessageField(note, 2) // Note.note_text
	if !ok {
		return nil, fmt.Errorf("no note_text field in note protobuf")
	}
	units := utf16.Encode([]rune(string(noteText)))

	result := make(map[string]bool)
	pos := 0
	lineStart := 0
	lineHasChecklist := false
	lineDone := false

	flush := func(end int) {
		text := normalizeChecklistText(string(utf16.Decode(units[lineStart:end])))
		if lineHasChecklist && text != "" {
			result[text] = lineDone
		}
		lineHasChecklist = false
		lineDone = false
	}

	forEachField(note, 5, func(runBytes []byte) bool { // Note.attribute_run
		length, _ := getVarintField(runBytes, 1) // AttributeRun.length
		if ps, ok := getMessageField(runBytes, 2); ok { // paragraph_style
			styleType, _ := getVarintField(ps, 1) // ParagraphStyle.style_type
			if cl, ok := getMessageField(ps, 5); ok && styleType == paragraphStyleChecklist {
				// A Checklist submessage can survive on a paragraph after
				// it's been converted back to a plain/dashed list (observed
				// on a real note: style_type 100 "dashed list" carrying a
				// leftover checklist.done from when it was style_type 103)
				// — Notes.app itself no longer renders that as a checkbox,
				// so style_type is the actual source of truth, not just the
				// Checklist submessage's presence.
				lineHasChecklist = true
				done, _ := getVarintField(cl, 2) // Checklist.done
				if done == 1 {
					lineDone = true
				}
			}
		}
		end := pos + int(length)
		if end > len(units) {
			end = len(units)
		}
		for pos < end {
			if units[pos] == '\n' {
				flush(pos)
				lineStart = pos + 1
			}
			pos++
		}
		return true
	})
	if lineStart < len(units) {
		flush(len(units))
	}

	return result, nil
}

// normalizeChecklistText matches how a checklist line's text will look once
// it's come back through StripHTML (HTML-entity-decoded, whitespace-trimmed)
// so a map lookup by that text reliably hits.
func normalizeChecklistText(s string) string {
	s = strings.ReplaceAll(s, " ", " ") // non-breaking space, as StripHTML does for &nbsp;
	s = strings.ReplaceAll(s, "&ampamp", "&")
	s = html.UnescapeString(html.UnescapeString(s))
	return strings.TrimSpace(s)
}

// ── Minimal protobuf wire-format helpers ────────────────────────────────────
//
// Hand-rolled instead of protoc-generated: the fields notectl needs (note
// text and per-paragraph checklist state) are a handful of scalars in an
// otherwise large, still-partially-unknown schema, so decoding just those
// tags directly with protowire avoids a codegen step for a file that would
// otherwise be mostly unused message types.

func getMessageField(data []byte, fieldNum int32) ([]byte, bool) {
	var found []byte
	ok := false
	forEachField(data, fieldNum, func(v []byte) bool {
		found = v
		ok = true
		return false // first match wins
	})
	return found, ok
}

func getVarintField(data []byte, fieldNum int32) (int64, bool) {
	b := data
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			return 0, false
		}
		b = b[n:]
		if int32(num) == fieldNum && typ == protowire.VarintType {
			val, n2 := protowire.ConsumeVarint(b)
			if n2 < 0 {
				return 0, false
			}
			return int64(val), true
		}
		nskip, ok := skipField(b, typ)
		if !ok {
			return 0, false
		}
		b = b[nskip:]
	}
	return 0, false
}

// forEachField calls fn with the raw bytes of every length-delimited
// occurrence of fieldNum at the top level of data, in order, stopping early
// if fn returns false.
func forEachField(data []byte, fieldNum int32, fn func([]byte) bool) {
	b := data
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			return
		}
		b = b[n:]
		if int32(num) == fieldNum && typ == protowire.BytesType {
			v, n2 := protowire.ConsumeBytes(b)
			if n2 < 0 {
				return
			}
			b = b[n2:]
			if !fn(v) {
				return
			}
			continue
		}
		nskip, ok := skipField(b, typ)
		if !ok {
			return
		}
		b = b[nskip:]
	}
}

func skipField(b []byte, typ protowire.Type) (int, bool) {
	switch typ {
	case protowire.VarintType:
		_, n := protowire.ConsumeVarint(b)
		return n, n >= 0
	case protowire.Fixed32Type:
		_, n := protowire.ConsumeFixed32(b)
		return n, n >= 0
	case protowire.Fixed64Type:
		_, n := protowire.ConsumeFixed64(b)
		return n, n >= 0
	case protowire.BytesType:
		_, n := protowire.ConsumeBytes(b)
		return n, n >= 0
	default:
		return 0, false
	}
}
