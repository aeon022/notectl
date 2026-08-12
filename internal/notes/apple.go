package notes

import (
	"fmt"
	"html"
	"os/exec"
	"strings"
	"time"

	"github.com/aeon022/notectl/internal/models"
)

// ListApple returns all notes from Apple Notes (optionally filtered by
// folder — the leaf folder name, matching this script's own fName equality
// check; see appleFolderParents for why full "Parent/Child" paths aren't
// resolved here). Every note's Folder field comes back as a full path
// (e.g. "Projects/Git") when it sits in a subfolder, built by pairing the
// flat per-note scan below with a separate, cheap folder→parent scan —
// AppleScript's "folders of <account>" already returns nested folders, but
// only ever exposes their own leaf name, never their container.
//
// The outer loop iterates every account and asks it for its own folders —
// deliberately not the global "every folder" this used before — because
// several real accounts share identically-named default top-level folders
// (a German-locale iCloud-adjacent account's "Notizen" vs. an English one's
// "Notes"; confirmed live: this machine has 7 accounts, 3 folders named
// "Notizen" and 4 named "Notes" across them). Scoping the note scan itself
// to each account's own folder list — rather than naming folders globally
// and attaching an account after the fact — is what makes the ACCOUNT
// field below unambiguous even when two accounts' folders collide by name.
func ListApple(folder string) ([]models.Note, error) {
	folderFilter := ""
	if folder != "" {
		folderFilter = fmt.Sprintf(`if fName is "%s" then`, escapeAS(folder))
	}
	script := fmt.Sprintf(`
tell application "Notes"
	set output to ""
	repeat with a in every account
		set aName to name of a
		set folderList to folders of a
		repeat with f in folderList
			set fName to name of f
			if fName is not "Recently Deleted" and fName is not "Zuletzt gelöscht" then
				%s
					set noteList to notes of f
					repeat with n in noteList
						set nID to id of n
						set nName to name of n
						set nMod to modification date of n

						set yr to year of nMod as string
						set mo to text -2 thru -1 of ("0" & ((month of nMod as integer) as string))
						set dy to text -2 thru -1 of ("0" & (day of nMod as string))
						set hr to text -2 thru -1 of ("0" & (hours of nMod as string))
						set mn to text -2 thru -1 of ("0" & (minutes of nMod as string))
						set sc to text -2 thru -1 of ("0" & (seconds of nMod as string))
						set nModStr to yr & "-" & mo & "-" & dy & "T" & hr & ":" & mn & ":" & sc
						set nBody to body of n

						set output to output & "ID:" & nID & linefeed
						set output to output & "TITLE:" & nName & linefeed
						set output to output & "FOLDER:" & fName & linefeed
						set output to output & "ACCOUNT:" & aName & linefeed
						set output to output & "MODTIME:" & nModStr & linefeed
						set output to output & "BODY:" & nBody & linefeed
						set output to output & "---NOTE---" & linefeed
					end repeat
				%s
			end if
		end repeat
	end repeat
	return output
end tell
`, folderFilter, map[bool]string{true: "end if", false: ""}[folder != ""])
	out, err := runAppleScript(script)
	if err != nil {
		return nil, err
	}
	notes := parseAppleNotes(out)
	if parents, perr := appleFolderParents(); perr == nil {
		for i := range notes {
			notes[i].Folder = appleFolderPath(notes[i].Folder, parents)
		}
	}
	return notes, nil
}

// appleFolderParents returns, for every Apple Notes folder, its immediate
// parent folder's leaf name — or "" if it sits directly under an account
// (i.e. it's a top-level folder, not a real subfolder). Keyed by leaf name
// like the rest of this file's folder handling; two folders that share a
// name under different parents/accounts are already indistinguishable
// elsewhere in this integration (FOLDER: only ever carries the leaf name),
// so this doesn't introduce a new ambiguity, just inherits the existing one.
func appleFolderParents() (map[string]string, error) {
	script := `
tell application "Notes"
	set output to ""
	set folderList to every folder
	repeat with f in folderList
		set fName to name of f
		set pName to ""
		try
			set c to container of f
			if (class of c) is folder then
				set pName to name of c
			end if
		end try
		set output to output & fName & tab & pName & linefeed
	end repeat
	return output
end tell
`
	out, err := runAppleScript(script)
	if err != nil {
		return nil, err
	}
	parents := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		if line = strings.TrimRight(line, "\r"); line == "" {
			continue
		}
		fields := strings.SplitN(line, "\t", 2)
		if len(fields) != 2 {
			continue
		}
		parents[fields[0]] = fields[1]
	}
	return parents, nil
}

// appleFolderPath walks the parent chain built by appleFolderParents and
// joins it into a "Parent/Child" path, e.g. "Git" under "Projects" becomes
// "Projects/Git". A folder with no parent (or one missing from the map)
// returns unchanged. seen guards against a cyclical container chain — not
// expected from real Notes.app data, but cheap to rule out.
func appleFolderPath(leaf string, parents map[string]string) string {
	if leaf == "" {
		return leaf
	}
	segs := []string{leaf}
	cur := leaf
	seen := map[string]bool{leaf: true}
	for {
		p := parents[cur]
		if p == "" || seen[p] {
			break
		}
		segs = append([]string{p}, segs...)
		seen[p] = true
		cur = p
	}
	return strings.Join(segs, "/")
}

// rawAppleID strips the "apple-" prefix models.Note.ID carries, recovering
// the raw AppleScript coredata id (e.g. "x-coredata://.../ICNote/p182") used
// to address the note directly. Looking notes up by this stable id — instead
// of by their mutable display name — is what makes renaming an existing note
// (rather than silently creating a duplicate under the new name) possible.
func rawAppleID(id string) string {
	return strings.TrimPrefix(id, "apple-")
}

// ReadApple fetches the full HTML body of a note by its stable id.
func ReadApple(id string) (string, error) {
	script := fmt.Sprintf(`
tell application "Notes"
	try
		return body of note id "%s"
	on error
		return ""
	end try
end tell
`, escapeAS(rawAppleID(id)))
	return runAppleScript(script)
}

// WriteApple creates or updates a note in Apple Notes. If id is non-empty,
// the existing note is updated in place by id; only its body is touched —
// Notes derives the displayed title from the body's first line, and directly
// setting the `name` property was found (via live testing) to desync from
// the rendered title rather than rename it, so it's deliberately left alone
// on update (callers are expected to already have the title as htmlBody's
// first line for updates, same as the rest of the note's content). If id
// is empty, a new note is created and its freshly assigned id is returned
// so the caller can persist it for future updates; on create, the title is
// prefixed onto htmlBody as its own line here — Apple Notes derives the
// displayed title from the body's first line the same way on create as on
// update, and a body that doesn't already start with the title produced a
// note whose title ended up duplicated into its own content (confirmed via
// live testing). Prefixing internally means callers just pass their real
// body; they don't need to know about this quirk.
func WriteApple(id, title, htmlBody, folder string) (string, error) {
	if id != "" {
		updateScript := fmt.Sprintf(`
tell application "Notes"
	try
		set body of note id "%s" to "%s"
	end try
end tell
`, escapeAS(rawAppleID(id)), escapeAS(htmlBody))
		_, err := runAppleScript(updateScript)
		return id, err
	}

	target := "default account"
	folderSetup := ""
	if folder != "" {
		folderSetup, target = appleFolderRefChain(folder)
	}
	fullBody := "<div>" + htmlEscape(title) + "</div><div><br></div>" + htmlBody
	createScript := fmt.Sprintf(`
tell application "Notes"%s
	set newNote to make new note at %s with properties {body:"%s"}
	return id of newNote
end tell
`, folderSetup, target, escapeAS(fullBody))
	out, err := runAppleScript(createScript)
	if err != nil {
		return "", err
	}
	return "apple-" + strings.TrimSpace(out), nil
}

// UpsertApple writes title/body to Apple Notes, updating an existing note in
// place if one with this exact title already exists in folder, or creating a
// new one otherwise. WriteApple itself never does this lookup — called with
// id "" it always creates, which is fine for the TUI (a human already picked
// an existing note's id off a list before editing it) but meant every
// notectl write / MCP write_note call against an already-used title silently
// spawned a duplicate note instead of updating it. This is the shared
// lookup-then-write path for those two callers.
func UpsertApple(title, body, folder string) (string, error) {
	// Deliberately ListApple("") + filter here in Go, not ListApple(folder):
	// the AppleScript folder filter matches a folder's own leaf name
	// (fName) against the caller's possibly-nested path ("Projects/X"),
	// which can never be equal for a real subfolder — it silently returns
	// zero notes for any nested folder. models.Note.Folder is populated
	// with the real resolved path afterwards (see appleFolderPath below),
	// so filtering on that post-resolution field here sidesteps the bug
	// instead of needing to fix ListApple's own script.
	existing, err := ListApple("")
	if err != nil {
		return "", err
	}
	var matchID string
	var matchMod time.Time
	for _, n := range existing {
		if n.Title == title && n.Folder == folder && (matchID == "" || n.ModTime.After(matchMod)) {
			matchID = n.ID
			matchMod = n.ModTime
		}
	}
	if matchID == "" {
		return WriteApple("", title, TextToHTML(body), folder)
	}
	// Update path: body's first line has to carry the title itself (see
	// WriteApple's doc comment) — TextToHTML(title + body) mirrors what the
	// create path does internally via its own title-div prefix.
	plainBody := title
	if body != "" {
		plainBody = title + "\n\n" + body
	}
	return WriteApple(matchID, title, TextToHTML(plainBody), folder)
}

// appleFolderRefChain turns a folder path like "Projects/Git" into an
// AppleScript snippet that creates every missing level (top-level "Projects"
// first, then "Git" inside it) and an AppleScript expression referring to
// the deepest one — for a single-segment path this reduces to exactly the
// old flat "if not (exists folder X) then make new folder..." behavior.
func appleFolderRefChain(path string) (setupScript, targetRef string) {
	containerRef := ""
	var b strings.Builder
	for _, seg := range strings.Split(path, "/") {
		if seg == "" {
			continue
		}
		esc := escapeAS(seg)
		var existsExpr, refExpr, atClause string
		if containerRef == "" {
			existsExpr = fmt.Sprintf(`exists folder "%s"`, esc)
			refExpr = fmt.Sprintf(`folder "%s"`, esc)
		} else {
			existsExpr = fmt.Sprintf(`exists folder "%s" of %s`, esc, containerRef)
			refExpr = fmt.Sprintf(`folder "%s" of %s`, esc, containerRef)
			atClause = fmt.Sprintf(" at %s", containerRef)
		}
		b.WriteString(fmt.Sprintf(`
	if not (%s) then
		make new folder%s with properties {name:"%s"}
	end if`, existsExpr, atClause, esc))
		containerRef = refExpr
	}
	if containerRef == "" {
		return "", "default account"
	}
	return b.String(), containerRef
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
func OpenApple(id string) error {
	script := fmt.Sprintf(`
tell application "Notes"
	activate
	try
		show note id "%s"
	end try
end tell
`, escapeAS(rawAppleID(id)))
	_, err := runAppleScript(script)
	return err
}

// UpdateBody writes an HTML body back to an existing Apple Notes note by id.
func UpdateBody(id, htmlBody string) error {
	script := fmt.Sprintf(`
tell application "Notes"
	try
		set body of note id "%s" to "%s"
	end try
end tell
`, escapeAS(rawAppleID(id)), escapeAS(htmlBody))
	_, err := runAppleScript(script)
	return err
}

// TextToHTML converts a plain-text note body (with ☐/☑ checkbox markers,
// Markdown headings, bullet lists, and inline styles) into Apple
// Notes–compatible HTML.
func TextToHTML(body string) string {
	lines := strings.Split(body, "\n")
	if looksLikeMarkdownTable(lines) {
		return tableMarkdownToHTML(lines)
	}
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
	checklistItem := func(text string, checked bool) {
		closeList()
		if !inChecklist {
			sb.WriteString(`<ul class="Apple-checked-list">`)
			inChecklist = true
		}
		cls := "Apple-unchecked"
		marker := "☐ "
		if checked {
			cls = "Apple-checked"
			marker = "☑ "
		}
		sb.WriteString(fmt.Sprintf(
			`<li><span class="Apple-checked-list-item %s">%s%s</span></li>`,
			cls, marker, mdInlineToHTML(text),
		))
	}
	bulletItem := func(text string) {
		closeChecklist()
		if !inList {
			sb.WriteString("<ul>")
			inList = true
		}
		sb.WriteString("<li>" + mdInlineToHTML(text) + "</li>")
	}

	for _, line := range lines {
		t := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(t, "☐ "):
			checklistItem(strings.TrimPrefix(t, "☐ "), false)
		case strings.HasPrefix(t, "☑ "):
			checklistItem(strings.TrimPrefix(t, "☑ "), true)
		case strings.HasPrefix(t, "- [ ] "):
			checklistItem(strings.TrimPrefix(t, "- [ ] "), false)
		case strings.HasPrefix(t, "* [ ] "):
			checklistItem(strings.TrimPrefix(t, "* [ ] "), false)
		case strings.HasPrefix(t, "- [x] "):
			checklistItem(strings.TrimPrefix(t, "- [x] "), true)
		case strings.HasPrefix(t, "- [X] "):
			checklistItem(strings.TrimPrefix(t, "- [X] "), true)
		case strings.HasPrefix(t, "* [x] "):
			checklistItem(strings.TrimPrefix(t, "* [x] "), true)
		case strings.HasPrefix(t, "* [X] "):
			checklistItem(strings.TrimPrefix(t, "* [X] "), true)
		case strings.HasPrefix(t, "• "):
			bulletItem(strings.TrimPrefix(t, "• "))
		case strings.HasPrefix(t, "- "):
			bulletItem(strings.TrimPrefix(t, "- "))
		case strings.HasPrefix(t, "* "):
			bulletItem(strings.TrimPrefix(t, "* "))
		case strings.HasPrefix(t, "# "):
			closeChecklist()
			closeList()
			sb.WriteString("<h1>" + mdInlineToHTML(t[2:]) + "</h1>")
		case strings.HasPrefix(t, "## "):
			closeChecklist()
			closeList()
			sb.WriteString("<h2>" + mdInlineToHTML(t[3:]) + "</h2>")
		case strings.HasPrefix(t, "### "):
			closeChecklist()
			closeList()
			sb.WriteString("<h3>" + mdInlineToHTML(t[4:]) + "</h3>")
		case t == "":
			closeChecklist()
			closeList()
			sb.WriteString("<div><br></div>")
		default:
			closeChecklist()
			closeList()
			sb.WriteString("<div>" + mdInlineToHTML(line) + "</div>")
		}
	}
	closeChecklist()
	closeList()
	return sb.String()
}

// mdInlineToHTML HTML-escapes a line of text and converts inline Markdown —
// **bold**, *italic*, ~~strike~~, `code` — into tags Apple Notes understands.
func mdInlineToHTML(s string) string {
	var out strings.Builder
	for i := 0; i < len(s); {
		if strings.HasPrefix(s[i:], "**") {
			if end := strings.Index(s[i+2:], "**"); end >= 0 {
				out.WriteString("<b>" + htmlEscape(s[i+2:i+2+end]) + "</b>")
				i += 2 + end + 2
				continue
			}
		}
		if strings.HasPrefix(s[i:], "~~") {
			if end := strings.Index(s[i+2:], "~~"); end >= 0 {
				out.WriteString("<strike>" + htmlEscape(s[i+2:i+2+end]) + "</strike>")
				i += 2 + end + 2
				continue
			}
		}
		if s[i] == '*' {
			if end := strings.Index(s[i+1:], "*"); end >= 0 && !strings.HasPrefix(s[i+1+end:], "**") {
				out.WriteString("<i>" + htmlEscape(s[i+1:i+1+end]) + "</i>")
				i += 1 + end + 1
				continue
			}
		}
		if s[i] == '`' {
			if end := strings.Index(s[i+1:], "`"); end >= 0 {
				out.WriteString("<tt>" + htmlEscape(s[i+1:i+1+end]) + "</tt>")
				i += 1 + end + 1
				continue
			}
		}
		switch s[i] {
		case '&':
			out.WriteString("&amp;")
		case '<':
			out.WriteString("&lt;")
		case '>':
			out.WriteString("&gt;")
		case '"':
			out.WriteString("&quot;")
		default:
			out.WriteByte(s[i])
		}
		i++
	}
	return out.String()
}

func htmlEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, `"`, "&quot;")
	return s
}

// DeleteApple moves a note to Trash in Apple Notes. Returns an error if no
// note with this id exists — the previous version wrapped the delete in a
// bare `try` with no failure path, so a stale/mismatched id (note already
// gone, wrong id format) silently did nothing while still reporting success
// to the caller, which then removed the local cache row regardless: a real
// Apple Note orphaned and untracked. Same bug class fixed in calctl's
// DeleteEvent on 2026-08-01.
func DeleteApple(id string) error {
	script := fmt.Sprintf(`
tell application "Notes"
	if exists note id "%s" then
		delete note id "%s"
	else
		error "note not found: %s"
	end if
end tell`, escapeAS(rawAppleID(id)), escapeAS(rawAppleID(id)), escapeAS(rawAppleID(id)))
	_, err := runAppleScript(script)
	return err
}

// StripHTML converts Apple Notes HTML to structured plain text.
// It preserves lists (• bullets), checklists (☐/☑), headings (# ##), and line breaks.
func StripHTML(s string) string {
	var out strings.Builder
	var tagBuf strings.Builder
	inTag := false
	lastNL := true          // treat start as newline so first line doesn't get a blank line
	inChecklist := false    // inside <ul class="Apple-checked-list">
	pendingBullet := ""     // bullet to emit before next text character
	headingRunOpen := false // true while inside a heading "line" that may be split
	// across several sibling <hN> runs (Apple Notes exports title text as
	// separate <h1>/<h2> runs per formatting change, e.g. one run for a
	// leading emoji and another for the rest of the text). Only the first
	// run in such a group opens the line; only the enclosing block close
	// (div/p/li/...) ends it. This also suppresses redundant <b>/<i> markers
	// inside a heading, which otherwise land as a stray "**" on their own
	// line because the heading tag boundaries don't align with them.

	// pendingMarker/activeMarker defer inline-style markers (**, *, ~~, `)
	// until real text actually arrives. Apple Notes commonly emits formatted
	// *empty* lines like <div><b><br></b></div> (a blank line that inherited
	// bold styling rather than actual bold text) — emitting the marker
	// immediately on tag-open would turn that into a stray "**" with nothing
	// to wrap. pendingMarker only becomes visible output (and activeMarker,
	// which owes a matching close) once emitStr/emitRune actually writes
	// something; a block boundary with no text in between just discards it.
	pendingMarker := ""
	activeMarker := ""

	// headingClosePending mirrors pendingMarker's "wait and see" approach but
	// for the newline a closed </hN> owes: Apple Notes splits one visual
	// title line into several sibling <hN> runs (see headingRunOpen above),
	// so </h2> alone can't tell whether the line is really over or another
	// <h2> run continues it. The close is deferred until something commits
	// it — a genuine newline, a new tag, or real text — at which point the
	// line is settled: it was never continued, so it ends now.
	headingClosePending := false

	emitNL := func() {
		pendingMarker = ""
		headingClosePending = false
		headingRunOpen = false
		if !lastNL {
			out.WriteByte('\n')
			lastNL = true
		}
	}
	flushPendingMarker := func() {
		if pendingMarker != "" {
			out.WriteString(pendingMarker)
			activeMarker = pendingMarker
			pendingMarker = ""
			lastNL = false
		}
	}
	commitHeadingClose := func() {
		if headingClosePending {
			headingClosePending = false
			headingRunOpen = false
			if !lastNL {
				out.WriteByte('\n')
				lastNL = true
			}
		}
	}
	emitStr := func(t string) {
		if len(t) == 0 {
			return
		}
		commitHeadingClose()
		// Bullet must land before any inline marker (☐ **bold**, not **☐ bold**).
		if pendingBullet != "" {
			if strings.HasPrefix(t, "☐") || strings.HasPrefix(t, "☑") || (pendingBullet == "• " && strings.HasPrefix(t, "•")) {
				pendingBullet = ""
			} else {
				out.WriteString(pendingBullet)
				pendingBullet = ""
				lastNL = false
			}
		}
		flushPendingMarker()
		out.WriteString(t)
		lastNL = t[len(t)-1] == '\n'
	}
	isFormattingWhitespace := func(r rune) bool {
		return r == '\n' || r == '\r' || r == '\t' || r == ' '
	}
	emitRune := func(r rune) {
		// HTML source formatting between tags (e.g. a literal newline
		// between "</li>\n<li>" purely for readability of the markup) is
		// insignificant whitespace, not content — every real HTML renderer
		// collapses it. Skipping it here when already at a fresh line and
		// nothing is pending stops it from being written as a second,
		// spurious newline (observed on a real note: a blank line between
		// every single checklist item that isn't actually in the note).
		if isFormattingWhitespace(r) && lastNL && pendingBullet == "" && pendingMarker == "" {
			return
		}
		commitHeadingClose()
		if pendingBullet != "" {
			if r == '☐' || r == '☑' || (pendingBullet == "• " && r == '•') {
				pendingBullet = ""
			} else {
				out.WriteString(pendingBullet)
				pendingBullet = ""
				lastNL = false
			}
		}
		flushPendingMarker()
		out.WriteRune(r)
		lastNL = r == '\n'
	}
	// openMarker stages an inline Markdown marker (**, *, `) to be emitted
	// only once real text follows it (see pendingMarker doc above).
	openMarker := func(mk string) {
		pendingMarker = mk
	}
	// closeMarker emits the matching close for a marker opened by openMarker,
	// but only if it was actually flushed (i.e. wrapped real text); an
	// open-with-no-content pair (pendingMarker still holding mk) is discarded
	// silently instead of leaving an unmatched marker in the output.
	closeMarker := func(mk string) {
		if pendingMarker == mk {
			pendingMarker = ""
			return
		}
		if activeMarker == mk {
			if pendingBullet != "" {
				out.WriteString(pendingBullet)
				pendingBullet = ""
			}
			out.WriteString(mk)
			activeMarker = ""
			lastNL = false
		}
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

		if !closing && pendingBullet != "" && len(attrs) > 0 {
			if strings.Contains(attrs, "apple-unchecked") || strings.Contains(attrs, "unchecked") {
				pendingBullet = "☐ "
			} else if strings.Contains(attrs, "apple-checked") || strings.Contains(attrs, "checked") {
				pendingBullet = "☑ "
			}
		}

		switch name {
		case "br":
			emitNL()

		case "p", "div", "blockquote", "pre", "table", "tr":
			if closing {
				emitNL()
			} else {
				commitHeadingClose()
				if name == "blockquote" {
					emitNL()
					emitStr("> ")
				}
			}

		case "h1", "h2", "h3", "h4", "h5", "h6":
			if closing {
				// Don't end the line yet — a sibling <hN> run (or a <b>/<i>
				// wrapper around one) may continue it. Deferred until
				// commitHeadingClose fires, from whatever comes next.
				headingClosePending = true
			} else if headingRunOpen && headingClosePending {
				// Continuation: a sibling run picking the same line back up.
				headingClosePending = false
			} else if !headingRunOpen {
				emitNL()
				headingRunOpen = true
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
				commitHeadingClose()
				if strings.Contains(attrs, "apple-checked-list") ||
					strings.Contains(attrs, "task-list") ||
					strings.Contains(attrs, "checklist") {
					inChecklist = true
				}
			}

		case "li":
			if !closing {
				headingRunOpen = false
				emitNL()
				if inChecklist || strings.Contains(attrs, "apple-checked") || strings.Contains(attrs, "apple-unchecked") || strings.Contains(attrs, "task-list") || strings.Contains(attrs, "checklist") {
					if strings.Contains(attrs, "apple-checked") || strings.Contains(attrs, "checked") {
						pendingBullet = "☑ "
					} else {
						pendingBullet = "☐ "
					}
				} else {
					pendingBullet = "• "
				}
			} else {
				pendingBullet = "" // discard if no text was in this li
				emitNL()
			}

		case "span", "font", "input", "object":
			// Apple Notes checklist: <span class="Apple-checked-list-item Apple-checked">
			if !closing && pendingBullet != "" {
				if strings.Contains(attrs, "apple-unchecked") || strings.Contains(attrs, "unchecked") {
					pendingBullet = "☐ "
				} else if strings.Contains(attrs, "apple-checked") || strings.Contains(attrs, "checked") {
					pendingBullet = "☑ "
				}
			}

		case "td", "th":
			if closing {
				emitStr("\t")
			}

		// inline styles → Markdown markers, deferred until real text follows
		// (see openMarker/closeMarker doc above). Suppressed entirely inside
		// a heading line: heading styling already implies emphasis, and
		// Apple Notes' <b>/<h2> tag boundaries for a title don't line up
		// with the actual text runs, which otherwise leaves a stray "**"
		// sitting on its own line.
		case "b", "strong":
			if !headingRunOpen {
				if closing {
					closeMarker("**")
				} else {
					openMarker("**")
				}
			}
		case "i", "em":
			if !headingRunOpen {
				if closing {
					closeMarker("*")
				} else {
					openMarker("*")
				}
			}
		case "s", "strike", "del":
			if !headingRunOpen {
				if closing {
					closeMarker("~~")
				} else {
					openMarker("~~")
				}
			}
		case "tt", "code":
			if !headingRunOpen {
				if closing {
					closeMarker("`")
				} else {
					openMarker("`")
				}
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
	result = strings.ReplaceAll(result, "&ampamp", "&")
	result = html.UnescapeString(html.UnescapeString(result))
	for strings.Contains(result, "\n\n\n") {
		result = strings.ReplaceAll(result, "\n\n\n", "\n\n")
	}
	result = cleanLineMarkers(result)
	result = collapseAdjacentMarkers(result)
	result = ensureEmojiSpaces(result)
	result = stripVariationSelectors(result)
	return strings.TrimSpace(result)
}

// stripVariationSelectors removes U+FE0E/U+FE0F (text/emoji presentation
// selectors) from note text. They're invisible hints, not content — but
// real-world impact showed up as a bug: at least one emoji in actual note
// content ("🛏️", bed + U+FE0F) is measured as one column wider by
// lipgloss/go's ansi width package than by go-runewidth, and every attempt
// to work around that disagreement downstream (padding differently, joining
// differently) just moved the same mismatch to a new symptom, because both
// paths ultimately rely on a width function that special-cases this
// selector. Removing the selector removes the disagreement at its source —
// terminal emoji fonts render the base codepoint as an emoji glyph with or
// without it regardless, so nothing is visually lost.
func stripVariationSelectors(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\uFE0E' || r == '\uFE0F' {
			return -1
		}
		return r
	}, s)
}

// collapseAdjacentMarkers merges back-to-back runs of the same inline marker
// (e.g. "**text1****text2**") into one continuous span ("**text1text2**").
// Apple Notes frequently splits a single visually-bold phrase into several
// adjacent <b> runs with identical styling (attribute-boundary artifacts of
// its rich text model), which round-trips through StripHTML as a close
// immediately followed by a re-open of the same marker with nothing between
// them — harmless once rendered, but needlessly noisy in the underlying text.
func collapseAdjacentMarkers(s string) string {
	for _, mk := range []string{"**", "~~", "`"} {
		s = strings.ReplaceAll(s, mk+mk, "")
	}
	return s
}

func cleanLineMarkers(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		trimmed := strings.TrimSpace(l)
		changed := true
		for changed {
			changed = false
			for _, p := range []struct{ prefix, repl string }{
				{"• ☐ ", "☐ "},
				{"• ☑ ", "☑ "},
				{"☐ ☐ ", "☐ "},
				{"☑ ☑ ", "☑ "},
				{"• • ", "• "},
				{"☐ • ", "☐ "},
				{"☑ • ", "☑ "},
			} {
				if strings.HasPrefix(trimmed, p.prefix) {
					trimmed = p.repl + strings.TrimPrefix(trimmed, p.prefix)
					changed = true
					break
				}
			}
		}
		idx := strings.IndexFunc(l, func(r rune) bool { return r != ' ' && r != '\t' })
		if idx > 0 && trimmed != "" {
			lines[i] = l[:idx] + trimmed
		} else if trimmed != "" {
			lines[i] = trimmed
		}
	}
	return strings.Join(lines, "\n")
}

func isEmojiRune(r rune) bool {
	if r == '☐' || r == '☑' || r == '✅' || r == '❌' || r == '•' || r == '📋' || r == '👶' || r == '🛒' || r == '🛏' || r == '🏥' {
		return true
	}
	switch {
	case r >= 0x1F300 && r <= 0x1FAFF:
		return true
	case r >= 0x2600 && r <= 0x27BF:
		return true
	case r >= 0x2300 && r <= 0x23FF:
		return true
	case r >= 0x2B00 && r <= 0x2BFF:
		return true
	case r >= 0x1F100 && r <= 0x1F2FF:
		return true
	}
	return false
}

func isEmojiModifier(r rune) bool {
	return r == 0xFE0F || r == 0xFE0E || (r >= 0x1F3FB && r <= 0x1F3FF) || r == 0x200D || r == 0x20E3
}

func ensureEmojiSpaces(s string) string {
	runes := []rune(s)
	var out []rune
	for i := 0; i < len(runes); i++ {
		out = append(out, runes[i])
		if isEmojiRune(runes[i]) {
			for i+1 < len(runes) && isEmojiModifier(runes[i+1]) {
				i++
				out = append(out, runes[i])
			}
			if i+1 < len(runes) {
				next := runes[i+1]
				if next != ' ' && next != '\t' && next != '\n' && next != '\r' && !isEmojiRune(next) && !isEmojiModifier(next) {
					out = append(out, ' ')
				}
			}
		}
	}
	return string(out)
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
		var appleID string
		var bodyLines []string
		for _, line := range strings.Split(block, "\n") {
			switch {
			case strings.HasPrefix(line, "ID:"):
				appleID = strings.TrimPrefix(line, "ID:")
			case strings.HasPrefix(line, "TITLE:"):
				n.Title = strings.TrimPrefix(line, "TITLE:")
			case strings.HasPrefix(line, "FOLDER:"):
				n.Folder = strings.TrimPrefix(line, "FOLDER:")
			case strings.HasPrefix(line, "ACCOUNT:"):
				n.Account = strings.TrimPrefix(line, "ACCOUNT:")
			case strings.HasPrefix(line, "MODTIME:"):
				t, _ := time.ParseInLocation("2006-01-02T15:04:05",
					strings.TrimPrefix(line, "MODTIME:"), time.Local)
				n.ModTime = t
				n.Created = t
			case strings.HasPrefix(line, "BODY:"):
				bodyLines = append(bodyLines, strings.TrimPrefix(line, "BODY:"))
			default:
				if len(bodyLines) > 0 {
					bodyLines = append(bodyLines, line)
				}
			}
		}
		if appleID == "" {
			continue
		}
		n.Title = strings.TrimSpace(n.Title)
		if n.Title == "" {
			n.Title = "Untitled"
		}
		n.ID = "apple-" + appleID
		if len(bodyLines) > 0 {
			rawBody := strings.Join(bodyLines, "\n")
			blocks := ParseBlocks(rawBody)
			n.Body = BlocksToPlain(blocks)
		}
		notes = append(notes, n)
	}
	return notes
}
