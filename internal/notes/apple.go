package notes

import (
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/aeon022/notectl/internal/models"
)

// ListApple returns all notes from Apple Notes (optionally filtered by folder).
func ListApple(folder string) ([]models.Note, error) {
	folderFilter := ""
	if folder != "" {
		folderFilter = fmt.Sprintf(`of folder "%s"`, escapeAS(folder))
	}
	script := fmt.Sprintf(`
tell application "Notes"
	set output to ""
	set noteList to every note %s
	repeat with n in noteList
		set nName to name of n
		set nMod to modification date of n
		set nFolder to ""
		try
			set nFolder to name of container of n
		end try
		set yr to year of nMod as string
		set mo to text -2 thru -1 of ("0" & ((month of nMod as integer) as string))
		set dy to text -2 thru -1 of ("0" & (day of nMod as string))
		set hr to text -2 thru -1 of ("0" & (hours of nMod as string))
		set mn to text -2 thru -1 of ("0" & (minutes of nMod as string))
		set sc to text -2 thru -1 of ("0" & (seconds of nMod as string))
		set nModStr to yr & "-" & mo & "-" & dy & "T" & hr & ":" & mn & ":" & sc
		set output to output & "TITLE:" & nName & linefeed
		set output to output & "FOLDER:" & nFolder & linefeed
		set output to output & "MODTIME:" & nModStr & linefeed
		set output to output & "---NOTE---" & linefeed
	end repeat
	return output
end tell
`, folderFilter)
	out, err := runAppleScript(script)
	if err != nil {
		return nil, err
	}
	return parseAppleNotes(out), nil
}

// ReadApple fetches the full body of a note by title.
func ReadApple(title string) (string, error) {
	script := fmt.Sprintf(`
tell application "Notes"
	set results to every note whose name is "%s"
	if (count of results) > 0 then
		return body of item 1 of results
	end if
	return ""
end tell
`, escapeAS(title))
	return runAppleScript(script)
}

// WriteApple creates or updates a note in Apple Notes.
func WriteApple(title, body, folder string) error {
	// Convert text (with ☐/☑ bullets) to Apple Notes HTML.
	htmlBody := TextToHTML(body)
	target := "default account"
	if folder != "" {
		target = fmt.Sprintf(`folder "%s"`, escapeAS(folder))
	}
	checkScript := fmt.Sprintf(`
tell application "Notes"
	set results to every note whose name is "%s"
	if (count of results) > 0 then
		return "exists"
	end if
	return "new"
end tell
`, escapeAS(title))
	state, err := runAppleScript(checkScript)
	if err != nil {
		return err
	}

	if strings.TrimSpace(state) == "exists" {
		updateScript := fmt.Sprintf(`
tell application "Notes"
	set results to every note whose name is "%s"
	if (count of results) > 0 then
		set body of item 1 of results to "%s"
	end if
end tell
`, escapeAS(title), escapeAS(htmlBody))
		_, err = runAppleScript(updateScript)
	} else {
		createScript := fmt.Sprintf(`
tell application "Notes"
	make new note at %s with properties {name:"%s", body:"%s"}
end tell
`, target, escapeAS(title), escapeAS(htmlBody))
		_, err = runAppleScript(createScript)
	}
	return err
}

// ListAppleFolders returns all folder names from Apple Notes.
func ListAppleFolders() ([]string, error) {
	script := `
tell application "Notes"
	set output to ""
	repeat with f in folders
		set output to output & (name of f) & linefeed
	end repeat
	return output
end tell
`
	out, err := runAppleScript(script)
	if err != nil {
		return nil, err
	}
	var folders []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			folders = append(folders, line)
		}
	}
	return folders, nil
}

// OpenApple brings the note up in the Apple Notes app.
func OpenApple(title string) error {
	script := fmt.Sprintf(`
tell application "Notes"
	activate
	set found to every note whose name is %q
	if (count of found) > 0 then
		show item 1 of found
	end if
end tell
`, title)
	_, err := runAppleScript(script)
	return err
}

// UpdateBody writes an HTML body back to an existing Apple Notes note.
func UpdateBody(title, htmlBody string) error {
	script := fmt.Sprintf(`
tell application "Notes"
	set found to every note whose name is "%s"
	if (count of found) > 0 then
		set body of item 1 of found to "%s"
	end if
end tell
`, escapeAS(title), escapeAS(htmlBody))
	_, err := runAppleScript(script)
	return err
}

// TextToHTML converts a plain-text note body (with ☐/☑ checkbox markers,
// Markdown headings, and bullet lists) into Apple Notes–compatible HTML.
func TextToHTML(body string) string {
	lines := strings.Split(body, "\n")
	var sb strings.Builder
	inChecklist := false
	inList := false

	closeChecklist := func() {
		if inChecklist {
			sb.WriteString("</ul>")
			inChecklist = false
		}
	}
	closeList := func() {
		if inList {
			sb.WriteString("</ul>")
			inList = false
		}
	}

	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "☐ ") || strings.HasPrefix(line, "☑ "):
			// native Apple Notes checklist item
			closeList()
			if !inChecklist {
				sb.WriteString(`<ul class="Apple-checked-list">`)
				inChecklist = true
			}
			checked := strings.HasPrefix(line, "☑ ")
			text := line[len("☐ "):]
			cls := "Apple-unchecked"
			if checked {
				cls = "Apple-checked"
			}
			sb.WriteString(fmt.Sprintf(
				`<li><span class="Apple-checked-list-item %s">%s</span></li>`,
				cls, htmlEscape(text),
			))
		case strings.HasPrefix(line, "• "):
			// regular bullet — unchecked
			closeChecklist()
			if !inList {
				sb.WriteString("<ul>")
				inList = true
			}
			sb.WriteString("<li>" + htmlEscape(line[len("• "):]) + "</li>")
		case strings.HasPrefix(line, "# "):
			closeChecklist(); closeList()
			sb.WriteString("<h1>" + htmlEscape(line[2:]) + "</h1>")
		case strings.HasPrefix(line, "## "):
			closeChecklist(); closeList()
			sb.WriteString("<h2>" + htmlEscape(line[3:]) + "</h2>")
		case strings.HasPrefix(line, "### "):
			closeChecklist(); closeList()
			sb.WriteString("<h3>" + htmlEscape(line[4:]) + "</h3>")
		case line == "":
			closeChecklist(); closeList()
			sb.WriteString("<div><br></div>")
		default:
			closeChecklist(); closeList()
			sb.WriteString("<div>" + htmlEscape(line) + "</div>")
		}
	}
	closeChecklist()
	closeList()
	return sb.String()
}

func htmlEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, `"`, "&quot;")
	return s
}

// DeleteApple moves a note to Trash in Apple Notes.
func DeleteApple(title string) error {
	script := fmt.Sprintf(`
tell application "Notes"
	set found to every note whose name is %q
	if (count of found) > 0 then
		delete item 1 of found
	end if
end tell`, title)
	_, err := runAppleScript(script)
	return err
}

// StripHTML converts Apple Notes HTML to structured plain text.
// It preserves lists (• bullets), checklists (☐/☑), headings (# ##), and line breaks.
func StripHTML(s string) string {
	var out strings.Builder
	var tagBuf strings.Builder
	inTag := false
	lastNL := true        // treat start as newline so first line doesn't get a blank line
	inChecklist := false  // inside <ul class="Apple-checked-list">
	pendingBullet := ""   // bullet to emit before next text character

	emitNL := func() {
		if !lastNL {
			out.WriteByte('\n')
			lastNL = true
		}
	}
	emitStr := func(t string) {
		out.WriteString(t)
		if len(t) > 0 {
			lastNL = t[len(t)-1] == '\n'
		}
	}
	emitRune := func(r rune) {
		if pendingBullet != "" {
			out.WriteString(pendingBullet)
			pendingBullet = ""
			lastNL = false
		}
		out.WriteRune(r)
		lastNL = r == '\n'
	}

	handleTag := func(raw string) {
		raw = strings.TrimSpace(raw)
		if raw == "" || strings.HasPrefix(raw, "!") {
			return
		}
		closing := strings.HasPrefix(raw, "/")
		if closing {
			raw = raw[1:]
		}
		// split into tag name + attributes
		sp := strings.IndexAny(raw, " \t\r\n")
		name := strings.ToLower(raw)
		attrs := ""
		if sp >= 0 {
			name = strings.ToLower(raw[:sp])
			attrs = strings.ToLower(raw[sp:])
		}

		switch name {
		case "br":
			emitNL()

		case "p", "div", "blockquote", "pre", "table", "tr":
			if closing {
				emitNL()
			} else if name == "blockquote" {
				emitNL()
				emitStr("> ")
			}

		case "h1", "h2", "h3", "h4", "h5", "h6":
			if closing {
				emitNL()
			} else {
				emitNL()
				switch name {
				case "h1":
					emitStr("# ")
				case "h2":
					emitStr("## ")
				case "h3":
					emitStr("### ")
				default:
					emitStr("#### ")
				}
			}

		case "ul", "ol":
			if closing {
				inChecklist = false
				pendingBullet = ""
				emitNL()
			} else {
				if strings.Contains(attrs, "apple-checked-list") ||
					strings.Contains(attrs, "task-list") ||
					strings.Contains(attrs, "checklist") {
					inChecklist = true
				}
			}

		case "li":
			if !closing {
				emitNL()
				if inChecklist {
					pendingBullet = "☐ " // may be updated by inner span
				} else {
					pendingBullet = "• "
				}
			} else {
				pendingBullet = "" // discard if no text was in this li
				emitNL()
			}

		case "span":
			// Apple Notes checklist: <span class="Apple-checked-list-item Apple-checked">
			if !closing && inChecklist && pendingBullet != "" {
				if strings.Contains(attrs, "apple-unchecked") {
					pendingBullet = "☐ "
				} else if strings.Contains(attrs, "apple-checked") {
					pendingBullet = "☑ "
				}
			}

		case "td", "th":
			if closing {
				emitStr("\t")
			}
		}
	}

	for _, r := range s {
		switch {
		case r == '<':
			inTag = true
			tagBuf.Reset()
		case r == '>' && inTag:
			inTag = false
			handleTag(tagBuf.String())
		case inTag:
			tagBuf.WriteRune(r)
		default:
			emitRune(r)
		}
	}

	result := out.String()
	result = strings.ReplaceAll(result, "&nbsp;", " ")
	result = strings.ReplaceAll(result, "&amp;", "&")
	result = strings.ReplaceAll(result, "&lt;", "<")
	result = strings.ReplaceAll(result, "&gt;", ">")
	result = strings.ReplaceAll(result, "&quot;", `"`)
	result = strings.ReplaceAll(result, "&#39;", "'")
	result = strings.ReplaceAll(result, "&apos;", "'")
	for strings.Contains(result, "\n\n\n") {
		result = strings.ReplaceAll(result, "\n\n\n", "\n\n")
	}
	return strings.TrimSpace(result)
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func runAppleScript(script string) (string, error) {
	out, err := exec.Command("osascript", "-e", script).Output()
	if err != nil {
		return "", fmt.Errorf("applescript: %w", err)
	}
	return strings.TrimRight(string(out), "\n"), nil
}

func escapeAS(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return s
}

func parseAppleNotes(out string) []models.Note {
	var notes []models.Note
	for _, block := range strings.Split(out, "---NOTE---\n") {
		block = strings.TrimSpace(block)
		if block == "" {
			continue
		}
		n := models.Note{Source: "apple"}
		for _, line := range strings.Split(block, "\n") {
			switch {
			case strings.HasPrefix(line, "TITLE:"):
				n.Title = strings.TrimPrefix(line, "TITLE:")
			case strings.HasPrefix(line, "FOLDER:"):
				n.Folder = strings.TrimPrefix(line, "FOLDER:")
			case strings.HasPrefix(line, "MODTIME:"):
				t, _ := time.ParseInLocation("2006-01-02T15:04:05",
					strings.TrimPrefix(line, "MODTIME:"), time.Local)
				n.ModTime = t
				n.Created = t
			}
		}
		if n.Title == "" {
			continue
		}
		n.ID = "apple-" + slugify(n.Title)
		notes = append(notes, n)
	}
	return notes
}
