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
	target := "default account"
	if folder != "" {
		target = fmt.Sprintf(`folder "%s"`, escapeAS(folder))
	}
	// check if note exists
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
`, escapeAS(title), escapeAS(body))
		_, err = runAppleScript(updateScript)
	} else {
		createScript := fmt.Sprintf(`
tell application "Notes"
	make new note at %s with properties {name:"%s", body:"%s"}
end tell
`, target, escapeAS(title), escapeAS(body))
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
