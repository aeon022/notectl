package notes

import (
	"regexp"
	"strings"
)

// Apple Notes renders tables as an <object><table>...</table></object>
// wrapper with fixed inline styling on every cell. This exact shape was
// captured live from Notes.app (see internal/notes doc comments on Block) —
// Notes re-parses whatever we hand back, but reusing its own markup keeps
// the round trip visually identical.
const tableCellStyle = `valign="top" style="border-style: solid; border-width: 1.0px 1.0px 1.0px 1.0px; border-color: #ccc; padding: 3.0px 5.0px 3.0px 5.0px; min-width: 70px"`

var (
	trRe   = regexp.MustCompile(`(?is)<tr[^>]*>(.*?)</tr>`)
	cellRe = regexp.MustCompile(`(?is)<t[dh][^>]*>(.*?)</t[dh]>`)
)

// tableBlockToMarkdown converts a top-level Apple Notes <table> block (as
// captured by ParseBlocks) into a GFM-style Markdown pipe table.
func tableBlockToMarkdown(raw string) string {
	rows := extractTableRows(raw)
	if len(rows) == 0 {
		return ""
	}
	cols := 0
	for _, r := range rows {
		if len(r) > cols {
			cols = len(r)
		}
	}
	var sb strings.Builder
	writeRow := func(r []string) {
		sb.WriteString("|")
		for i := 0; i < cols; i++ {
			cell := ""
			if i < len(r) {
				cell = r[i]
			}
			sb.WriteString(" " + cell + " |")
		}
		sb.WriteString("\n")
	}
	writeRow(rows[0])
	sb.WriteString("|")
	for i := 0; i < cols; i++ {
		sb.WriteString(" --- |")
	}
	sb.WriteString("\n")
	for _, r := range rows[1:] {
		writeRow(r)
	}
	return strings.TrimRight(sb.String(), "\n")
}

func extractTableRows(raw string) [][]string {
	var rows [][]string
	for _, trMatch := range trRe.FindAllStringSubmatch(raw, -1) {
		var cells []string
		for _, cellMatch := range cellRe.FindAllStringSubmatch(trMatch[1], -1) {
			cells = append(cells, tableCellText(cellMatch[1]))
		}
		if cells != nil {
			rows = append(rows, cells)
		}
	}
	return rows
}

func tableCellText(inner string) string {
	t := StripHTML(inner)
	t = strings.ReplaceAll(t, "\n", " ")
	t = strings.ReplaceAll(t, "|", `\|`)
	return strings.TrimSpace(t)
}

// looksLikeMarkdownTable reports whether every non-blank line of a block is
// pipe-delimited and the second line is a GFM header separator.
func looksLikeMarkdownTable(lines []string) bool {
	var rows []string
	for _, l := range lines {
		if t := strings.TrimSpace(l); t != "" {
			rows = append(rows, t)
		}
	}
	if len(rows) < 2 {
		return false
	}
	for _, r := range rows {
		if !strings.HasPrefix(r, "|") {
			return false
		}
	}
	return isTableSeparator(rows[1])
}

func isTableSeparator(line string) bool {
	hasDash := false
	for _, c := range line {
		switch c {
		case '|', '-', ':', ' ', '\t':
			if c == '-' {
				hasDash = true
			}
		default:
			return false
		}
	}
	return hasDash
}

// splitTableCells splits one pipe-delimited row into its cell texts,
// honoring backslash-escaped pipes (as emitted by tableCellText).
func splitTableCells(line string) []string {
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, "|")
	line = strings.TrimSuffix(line, "|")
	var cells []string
	var cur strings.Builder
	esc := false
	for _, r := range line {
		switch {
		case esc:
			cur.WriteRune(r)
			esc = false
		case r == '\\':
			esc = true
		case r == '|':
			cells = append(cells, strings.TrimSpace(cur.String()))
			cur.Reset()
		default:
			cur.WriteRune(r)
		}
	}
	cells = append(cells, strings.TrimSpace(cur.String()))
	return cells
}

// tableMarkdownToHTML renders a Markdown pipe table (lines already known to
// satisfy looksLikeMarkdownTable) as an Apple Notes table block.
func tableMarkdownToHTML(lines []string) string {
	var rows [][]string
	maxCols := 0
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l == "" || isTableSeparator(l) {
			continue
		}
		cells := splitTableCells(l)
		if len(cells) > maxCols {
			maxCols = len(cells)
		}
		rows = append(rows, cells)
	}

	var sb strings.Builder
	sb.WriteString(`<div><object><table cellspacing="0" cellpadding="0" style="border-collapse: collapse; direction: ltr">` + "\n<tbody>\n")
	for _, r := range rows {
		sb.WriteString("<tr>")
		for c := 0; c < maxCols; c++ {
			text := ""
			if c < len(r) {
				text = r[c]
			}
			sb.WriteString("<td " + tableCellStyle + ">")
			if text == "" {
				sb.WriteString("<br>")
			} else {
				sb.WriteString("<div>" + mdInlineToHTML(text) + "</div>")
			}
			sb.WriteString("</td>")
		}
		sb.WriteString("</tr>\n")
	}
	sb.WriteString("</tbody>\n</table></object></div>")
	return sb.String()
}
