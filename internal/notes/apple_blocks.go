package notes

import (
	"regexp"
	"strings"
)

// Block is one top-level structural unit of an Apple Notes HTML body — a
// paragraph, heading, list, or table. RawHTML is the exact source markup;
// Plain is its Markdown/plain-text rendering for editing.
//
// AppleScript's `body of note` property does not expose whether a <ul> is a
// genuine checklist or a plain bullet list, nor per-item checked state (this
// was confirmed by inspecting a real checklist note: identical <ul><li>
// markup either way). notectl therefore cannot reconstruct that formatting
// once it's downgraded to plain text. Blocks exist to avoid downgrading it in
// the first place: as long as the user's edit doesn't touch a given block,
// ReconcileBlocks writes its RawHTML back verbatim instead of regenerating it
// from the lossy plain-text form, so untouched checklists/tables elsewhere in
// the note survive an edit.
type Block struct {
	RawHTML string
	Plain   string
}

var voidTags = map[string]bool{
	"br": true, "img": true, "hr": true, "meta": true, "input": true,
}

// ParseBlocks splits an Apple Notes HTML body into top-level blocks.
func ParseBlocks(html string) []Block {
	var blocks []Block
	for _, raw := range splitTopLevelElements(html) {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		var plain string
		if strings.Contains(strings.ToLower(raw), "<table") {
			plain = tableBlockToMarkdown(raw)
		} else {
			plain = StripHTML(raw)
		}
		blocks = append(blocks, Block{RawHTML: raw, Plain: plain})
	}
	return blocks
}

// BlocksToPlain renders a block list into flattened plain text, blocks
// separated by a blank line — the form shown in the editor.
func BlocksToPlain(blocks []Block) string {
	var parts []string
	for _, b := range blocks {
		if b.Plain != "" {
			parts = append(parts, b.Plain)
		}
	}
	return strings.Join(parts, "\n\n")
}

// RenderPlain converts Apple Notes HTML straight to display/editable plain
// text (equivalent to BlocksToPlain(ParseBlocks(html)), but callers that
// don't need to save shouldn't have to care about blocks).
func RenderPlain(html string) string {
	return BlocksToPlain(ParseBlocks(html))
}

// splitTopLevelElements splits an HTML fragment into its top-level elements
// (plus any bare top-level text runs), preserving exact source markup. Apple
// Notes bodies are always a flat sequence of such elements (<div>, <h1-6>,
// <ul>/<ol>, or a table-wrapping <div>) with no top-level nesting ambiguity,
// so a simple balanced-depth counter is sufficient — no tag-name stack needed.
func splitTopLevelElements(html string) []string {
	var out []string
	depth := 0
	start := 0
	i := 0
	n := len(html)

	flush := func(end int) {
		if s := strings.TrimSpace(html[start:end]); s != "" {
			out = append(out, s)
		}
		start = end
	}

	for i < n {
		lt := strings.IndexByte(html[i:], '<')
		if lt < 0 {
			break
		}
		lt += i

		if depth == 0 && strings.TrimSpace(html[start:lt]) != "" {
			flush(lt)
		}

		gt := strings.IndexByte(html[lt:], '>')
		if gt < 0 {
			break
		}
		gt += lt
		tagBody := html[lt+1 : gt]

		closing := strings.HasPrefix(tagBody, "/")
		trimmed := strings.TrimSpace(tagBody)
		selfClose := strings.HasSuffix(trimmed, "/")
		name := tagBody
		if closing {
			name = name[1:]
		}
		name = strings.TrimSuffix(strings.TrimSpace(name), "/")
		if sp := strings.IndexAny(name, " \t\r\n"); sp >= 0 {
			name = name[:sp]
		}
		name = strings.ToLower(strings.TrimSpace(name))
		isVoid := voidTags[name] || selfClose || strings.HasPrefix(tagBody, "!")

		i = gt + 1

		switch {
		case isVoid:
			if depth == 0 {
				flush(i)
			}
		case closing:
			if depth > 0 {
				depth--
			}
			if depth == 0 {
				flush(i)
			}
		default:
			depth++
		}
	}
	if strings.TrimSpace(html[start:]) != "" {
		out = append(out, html[start:])
	}
	return out
}

var blankLineRe = regexp.MustCompile(`\n{2,}`)

// splitPlainBlocks splits edited editor text on blank-line boundaries,
// mirroring how BlocksToPlain joins blocks back together.
func splitPlainBlocks(text string) []string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	var out []string
	for _, s := range blankLineRe.Split(text, -1) {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// lcsIndices aligns two string sequences by their longest common subsequence
// of exactly-equal elements, returning the segment-index -> original-index
// matches (in b's index space). Used to tell which edited blocks are
// untouched copies of an original block (and can reuse its RawHTML) versus
// new or changed content (which must be regenerated).
func lcsIndices(a, b []string) map[int]int {
	n, m := len(a), len(b)
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}
	matchB := map[int]int{}
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			matchB[j] = i
			i++
			j++
		case dp[i+1][j] >= dp[i][j+1]:
			i++
		default:
			j++
		}
	}
	return matchB
}

// ReconcileBlocks rebuilds an Apple Notes HTML body from edited plain text.
// Segments that still match an original block's Plain text verbatim are
// written back using that block's original RawHTML byte-for-byte; anything
// new or changed is regenerated with TextToHTML. This is what keeps an edit
// to one part of a note from clobbering formatting notectl can't fully
// model (genuine checklists, tables) elsewhere in the same note.
func ReconcileBlocks(original []Block, editedPlain string) string {
	segments := splitPlainBlocks(editedPlain)
	if len(segments) == 0 {
		return ""
	}

	origPlain := make([]string, len(original))
	for i, b := range original {
		origPlain[i] = b.Plain
	}
	matches := lcsIndices(origPlain, segments)

	parts := make([]string, 0, len(segments))
	for si, seg := range segments {
		if oi, ok := matches[si]; ok {
			parts = append(parts, original[oi].RawHTML)
			continue
		}
		parts = append(parts, TextToHTML(seg))
	}
	return strings.Join(parts, "\n<div><br></div>\n")
}
