package tui

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/aeon022/notectl/internal/config"
	"github.com/aeon022/notectl/internal/models"
	"github.com/aeon022/notectl/internal/notes"
	"github.com/aeon022/notectl/internal/store"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
	"github.com/muesli/reflow/wrap"
)

// ── Views ─────────────────────────────────────────────────────────────────────

type view int

const (
	viewList     view = iota
	viewDetail   view = iota
	viewNew      view = iota
	viewSettings view = iota
)

// ── Styles ────────────────────────────────────────────────────────────────────

var (
	colorBlue   = lipgloss.AdaptiveColor{Light: "25", Dark: "33"}
	colorGreen  = lipgloss.AdaptiveColor{Light: "28", Dark: "42"}
	colorRed    = lipgloss.AdaptiveColor{Light: "160", Dark: "203"}
	colorMuted  = lipgloss.AdaptiveColor{Light: "243", Dark: "246"}
	colorSubtle = lipgloss.AdaptiveColor{Light: "250", Dark: "239"}
	colorTabBg  = lipgloss.AdaptiveColor{Light: "252", Dark: "235"}

	styleHeader   = lipgloss.NewStyle().Bold(true).Foreground(colorBlue)
	styleDivider  = lipgloss.NewStyle().Foreground(colorSubtle)
	styleHelp     = lipgloss.NewStyle().Foreground(colorMuted)
	styleErr      = lipgloss.NewStyle().Foreground(colorRed)
	styleOK       = lipgloss.NewStyle().Foreground(colorGreen)
	styleMuted    = lipgloss.NewStyle().Foreground(colorMuted)
	styleBold     = lipgloss.NewStyle().Bold(true)
	styleSelected = lipgloss.NewStyle().
			Background(lipgloss.AdaptiveColor{Light: "189", Dark: "17"}).
			Foreground(lipgloss.AdaptiveColor{Light: "16", Dark: "255"}).
			Bold(true)
	styleTag     = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "33", Dark: "75"})
	styleFolder  = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "136", Dark: "178"})
	styleLabel   = lipgloss.NewStyle().Foreground(colorBlue).Width(9)
	styleSyncing = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "214", Dark: "220"})

	styleTabActive = lipgloss.NewStyle().Bold(true).
			Foreground(lipgloss.Color("15")).
			Background(colorBlue).
			Padding(0, 3)
	styleTabInact = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "237", Dark: "252"}).
			Background(colorTabBg).
			Padding(0, 3)

	// markdown
	styleMDH1    = lipgloss.NewStyle().Bold(true).Foreground(colorBlue)
	styleMDH2    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.AdaptiveColor{Light: "26", Dark: "39"})
	styleMDH3    = lipgloss.NewStyle().Bold(true)
	styleMDQuote = lipgloss.NewStyle().Foreground(colorMuted)
	styleMDCode  = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "130", Dark: "215"})
	styleMDBold  = lipgloss.NewStyle().Bold(true)
	styleStrike  = lipgloss.NewStyle().Strikethrough(true).Foreground(colorMuted)

	// date age colors — amber for today (matches mailctl styleToday), fading to subtle
	styleDateToday = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "214", Dark: "220"}).Bold(true)
	styleDateWeek  = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "243", Dark: "246"})
	styleDateMonth = lipgloss.NewStyle().Foreground(colorMuted)
	styleDateOld   = lipgloss.NewStyle().Foreground(colorSubtle)
)

// sourceTypes is the ordered list of source backends for the settings view.
var sourceTypes = []struct {
	key   config.SourceType
	label string
	note  string
}{
	{config.SourceApple, "Apple Notes", "syncs from Apple Notes via AppleScript"},
	{config.SourceObsidian, "Obsidian", "reads .md files with YAML frontmatter"},
	{config.SourceMarkdown, "Markdown", "any folder of plain .md files"},
	{config.SourceJoplin, "Joplin", "coming soon — Joplin exported notes"},
}

// ── Messages ──────────────────────────────────────────────────────────────────

type notesLoadedMsg struct {
	notes        []models.Note
	folders      []string
	folderCounts map[string]int
}
type syncDoneMsg struct {
	count int
	err   error
}
type writeDoneMsg struct {
	note *models.Note
	err  error
}
type deletedMsg struct{ err error }
type savedSettingsMsg struct{ err error }
type appleBodyMsg struct {
	id     string
	body   string // raw Apple Notes HTML
	err    error
	goEdit bool // if true, open edit view after body is loaded
}
type errMsg struct{ err error }

// ── Model ─────────────────────────────────────────────────────────────────────

type Model struct {
	view   view
	width  int
	height int

	// list
	notes        []models.Note
	cursor       int
	searchQ      string
	searching    bool
	searchInput  textinput.Model
	folders      []string
	activeTab    int // 0 = All, 1+ = folder
	folderCounts map[string]int

	// detail / preview
	detail           *models.Note
	detailLineCursor int // current line in detail body (for j/k + checkbox toggle)
	detailYOffset    int // current visual Y offset in detail view
	detailBlocks     []notes.Block // Apple HTML blocks backing m.detail.Body, for non-destructive saves
	vp               viewport.Model
	pvp              viewport.Model // two-pane preview (right side)

	// new note
	titleInput    textinput.Model
	tagsInput     textinput.Model
	bodyArea      textarea.Model
	newFocus      int
	editNote      *models.Note
	editBlocks    []notes.Block // Apple HTML blocks backing the note being edited (nil for new notes)
	editorYOffset int // mirrors bodyArea's internal viewport scroll (for mouse clicks)

	// settings
	vaultInput textinput.Model
	sourceIdx  int

	// list options
	sortByDate bool    // true = mod_time desc (default), false = title asc
	paneRatio  float64 // two-pane left width ratio (default 0.38)
	confirmID  string  // non-empty = waiting for delete confirmation

	// status
	status     string
	statusTime time.Time
	err        error
	syncing    bool
	sp         spinner.Model
}

func New() Model {
	sp := spinner.New()
	sp.Spinner = spinner.MiniDot
	sp.Style = styleSyncing

	si := textinput.New()
	si.Placeholder = "search notes…"
	si.CharLimit = 200

	ti := textinput.New()
	ti.Placeholder = "Note title"
	ti.CharLimit = 200
	ti.Focus()

	tags := textinput.New()
	tags.Placeholder = "tag1, tag2 (optional)"
	tags.CharLimit = 200

	body := textarea.New()
	body.Placeholder = "Write your note here…"
	body.ShowLineNumbers = false

	vi := textinput.New()
	vi.Placeholder = "~/Documents/MyVault"
	vi.CharLimit = 500
	vi.SetValue(config.VaultPathRaw())

	srcIdx := 0
	current := config.Source()
	for i, s := range sourceTypes {
		if s.key == current {
			srcIdx = i
			break
		}
	}

	return Model{
		sp:          sp,
		searchInput: si,
		titleInput:  ti,
		tagsInput:   tags,
		bodyArea:    body,
		vaultInput:  vi,
		sourceIdx:   srcIdx,
		sortByDate:  true,
		paneRatio:   0.38,
	}
}

func Run() error {
	m := New()
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	_, err := p.Run()
	return err
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(loadNotesCmd("", ""), doSyncCmd(), tea.WindowSize())
}

func (m Model) activeFolder() string {
	if m.activeTab == 0 || m.activeTab >= len(m.folders)+1 {
		return ""
	}
	return m.folders[m.activeTab-1]
}

func (m Model) isTwoPane() bool { return m.width >= 100 }
func (m Model) leftWidth() int {
	if m.isTwoPane() {
		r := m.paneRatio
		if r <= 0 {
			r = 0.38
		}
		return min(int(float64(m.width)*r), m.width-30)
	}
	return m.width
}
func (m Model) pvpWidth() int { return m.width - m.leftWidth() - 1 }

// editorBodyWidth is the textarea width in the new/edit view — the left pane
// when the live preview is shown, full width otherwise.
func (m Model) editorBodyWidth() int {
	if m.isTwoPane() {
		return m.leftWidth() - 4
	}
	return m.width - 4
}

// ── Update ────────────────────────────────────────────────────────────────────

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.vp = viewport.New(msg.Width, m.bodyHeight())
		m.pvp = viewport.New(m.pvpWidth(), m.height-3)
		m.bodyArea.SetWidth(m.editorBodyWidth())
		m.bodyArea.SetHeight(m.height - 11)

	case notesLoadedMsg:
		// Remember which note was selected so we can restore it after the list changes
		// (e.g. after a sync that reorders notes by mod_time).
		var prevID string
		if m.cursor < len(m.notes) {
			prevID = m.notes[m.cursor].ID
		}
		if m.view == viewDetail && m.detail != nil {
			prevID = m.detail.ID
		}
		m.notes = msg.notes
		m.folders = msg.folders
		if msg.folderCounts != nil {
			m.folderCounts = msg.folderCounts
		}
		// Try to restore cursor to the same note by ID.
		found := false
		if prevID != "" {
			for i, n := range m.notes {
				if n.ID == prevID {
					m.cursor = i
					found = true
					break
				}
			}
		}
		if !found && m.cursor >= len(m.notes) {
			m.cursor = max(0, len(m.notes)-1)
		}
		m = m.applySortOrder()
		var pvCmd tea.Cmd
		m, pvCmd = m.refreshPreview()
		return m, pvCmd

	case syncDoneMsg:
		m.syncing = false
		if msg.err != nil {
			m.err = msg.err
		} else {
			m.setStatus(fmt.Sprintf("Synced %d notes", msg.count))
			return m, loadNotesCmd(m.searchQ, m.activeFolder())
		}

	case writeDoneMsg:
		if msg.err != nil {
			m.err = msg.err
		} else {
			name := ""
			if msg.note != nil {
				name = msg.note.Title
			}
			m.setStatus("Saved: " + name)
			m.view = viewList
			return m, loadNotesCmd(m.searchQ, m.activeFolder())
		}

	case deletedMsg:
		if msg.err != nil {
			m.err = msg.err
		}

	case savedSettingsMsg:
		if msg.err != nil {
			m.err = msg.err
		} else {
			m.setStatus("Settings saved")
			m.view = viewList
			return m, loadNotesCmd(m.searchQ, m.activeFolder())
		}

	case appleBodyMsg:
		if msg.err == nil {
			blocks := notes.ParseBlocks(msg.body)
			body := notes.BlocksToPlain(blocks)
			setChecklistStateFor(msg.id)
			// cache in notes slice
			var cachedNote *models.Note
			for i := range m.notes {
				if m.notes[i].ID == msg.id {
					m.notes[i].Body = body
					cachedNote = &m.notes[i]
					break
				}
			}
			// update detail view if open
			if m.detail != nil && m.detail.ID == msg.id {
				m.detail.Body = body
				m.detailBlocks = blocks
				content, visualCursor := renderDetailBody(body, m.detailLineCursor, m.detailBodyWidth())
				// Adjust offset if cursor went off screen due to length change
				if visualCursor >= m.detailYOffset+m.vp.Height {
					m.detailYOffset = visualCursor - m.vp.Height + 1
				}
				m.vp.SetContent(content)
				m.vp.SetYOffset(m.detailYOffset)
			}
			// update preview pane if still on same note
			if len(m.notes) > 0 && m.notes[m.cursor].ID == msg.id {
				m.pvp.SetContent(renderMarkdown(body, m.pvpWidth()))
				m.pvp.GotoTop()
			}
			// if this load was triggered by pressing e, open edit view now
			if msg.goEdit && cachedNote != nil {
				m.status = ""
				n := *cachedNote
				m.editNote = &n
				m.editBlocks = blocks
				title, rest := splitTitleBlock(blocks)
				m.resetNew(title)
				m.titleInput.SetValue(title)
				m.tagsInput.SetValue(strings.Join(n.Tags, ", "))
				m.bodyArea.SetValue(rest)
				m.view = viewNew
				return m, nil
			}
		} else if msg.err != nil {
			m.err = msg.err
		}

	case errMsg:
		m.err = msg.err

	case tea.MouseMsg:
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			if m.view == viewList {
				if m.cursor > 0 {
					m.cursor--
					var cmd tea.Cmd
					m, cmd = m.refreshPreview()
					return m, cmd
				}
			} else if m.view == viewDetail && m.detail != nil {
				if m.detailLineCursor > 0 {
					m.detailLineCursor--
					m = m.syncDetailViewport()
				}
			} else if m.view == viewNew && m.newFocus == 2 {
				return m.scrollEditor(-3), nil
			}
		case tea.MouseButtonWheelDown:
			if m.view == viewList {
				if m.cursor < len(m.notes)-1 {
					m.cursor++
					var cmd tea.Cmd
					m, cmd = m.refreshPreview()
					return m, cmd
				}
			} else if m.view == viewDetail && m.detail != nil {
				lines := strings.Split(m.detail.Body, "\n")
				if m.detailLineCursor < len(lines)-1 {
					m.detailLineCursor++
					m = m.syncDetailViewport()
				}
			} else if m.view == viewNew && m.newFocus == 2 {
				return m.scrollEditor(3), nil
			}
		case tea.MouseButtonLeft:
			if m.view == viewNew && msg.Action == tea.MouseActionPress {
				return m.handleEditorClick(msg.X, msg.Y), nil
			}
		}

	case spinner.TickMsg:
		if m.syncing {
			var cmd tea.Cmd
			m.sp, cmd = m.sp.Update(msg)
			return m, cmd
		}
		return m, nil

	case tea.KeyMsg:
		m.err = nil
		if time.Since(m.statusTime) > 4*time.Second {
			m.status = ""
		}
		switch m.view {
		case viewList:
			return m.updateList(msg)
		case viewDetail:
			return m.updateDetail(msg)
		case viewNew:
			return m.updateNew(msg)
		case viewSettings:
			return m.updateSettings(msg)
		}
	}

	if m.view == viewDetail {
		var cmd tea.Cmd
		m.vp, cmd = m.vp.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m Model) updateList(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.searching {
		switch msg.String() {
		case "enter":
			m.searchQ = m.searchInput.Value()
			m.searching = false
			m.cursor = 0
			return m, loadNotesCmd(m.searchQ, m.activeFolder())
		case "esc":
			m.searching = false
			m.searchInput.SetValue("")
			m.searchQ = ""
			m.cursor = 0
			return m, loadNotesCmd("", m.activeFolder())
		default:
			var cmd tea.Cmd
			m.searchInput, cmd = m.searchInput.Update(msg)
			return m, tea.Batch(cmd, loadNotesCmd(m.searchInput.Value(), m.activeFolder()))
		}
	}

	// pending delete confirmation — any key other than d/esc cancels
	if m.confirmID != "" && msg.String() != "d" && msg.String() != "esc" {
		m.confirmID = ""
		m.status = ""
	}

	prevCursor := m.cursor
	var extraCmd tea.Cmd

	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "tab":
		tabs := len(m.folders) + 1
		m.activeTab = (m.activeTab + 1) % tabs
		m.cursor = 0
		return m, loadNotesCmd(m.searchQ, m.activeFolder())
	case "shift+tab":
		tabs := len(m.folders) + 1
		m.activeTab = (m.activeTab - 1 + tabs) % tabs
		m.cursor = 0
		return m, loadNotesCmd(m.searchQ, m.activeFolder())
	case "j", "down":
		if m.cursor < len(m.notes)-1 {
			m.cursor++
		}
	case "k", "up":
		if m.cursor > 0 {
			m.cursor--
		}
	case "pgdown", "ctrl+f":
		m.cursor = min(len(m.notes)-1, m.cursor+max(1, m.height/3))
	case "pgup", "ctrl+b":
		m.cursor = max(0, m.cursor-max(1, m.height/3))
	case "g":
		m.cursor = 0
	case "G":
		m.cursor = max(0, len(m.notes)-1)
	case "enter":
		if len(m.notes) > 0 {
			n := m.notes[m.cursor]
			m.detail = &n
			m.detailBlocks = nil
			m.detailLineCursor = 0
			m.detailYOffset = 0
			m.vp.GotoTop()
			m.view = viewDetail
			if config.Source() == config.SourceApple {
				// Always re-fetch: detailBlocks must reflect this exact note,
				// and a plain-text-only cache hit wouldn't carry blocks along.
				m.vp.SetContent(styleMuted.Render("Loading…"))
				return m, loadAppleBodyCmd(n.ID)
			}
			content, _ := renderDetailBody(n.Body, 0, m.detailBodyWidth())
			m.vp.SetContent(content)
			return m, nil
		}
	case "n":
		m.editNote = nil
		m.resetNew("")
		m.view = viewNew
	case "e":
		if len(m.notes) > 0 {
			n := m.notes[m.cursor]
			if config.Source() == config.SourceApple && n.Body == "" {
				// load body first, then open edit
				m.setStatus("Loading…")
				return m, loadAppleBodyForEditCmd(n.ID)
			}
			m.editNote = &n
			m.resetNew(n.Title)
			m.titleInput.SetValue(n.Title)
			m.tagsInput.SetValue(strings.Join(n.Tags, ", "))
			m.bodyArea.SetValue(n.Body)
			m.view = viewNew
		}
	case "o":
		if len(m.notes) > 0 {
			n := m.notes[m.cursor]
			return m, openExternalCmd(n.ID, n.Title, n.Path)
		}
	case "d":
		if len(m.notes) > 0 {
			n := m.notes[m.cursor]
			if m.confirmID != n.ID {
				m.confirmID = n.ID
				m.setStatus(fmt.Sprintf("Delete \"%s\"?  d:confirm  esc:cancel", runeLimit(n.Title, 30)))
				return m, nil
			}
			// confirmed
			m.confirmID = ""
			m.notes = append(m.notes[:m.cursor], m.notes[m.cursor+1:]...)
			if m.cursor >= len(m.notes) {
				m.cursor = max(0, len(m.notes)-1)
			}
			m.setStatus("Deleted: " + n.Title)
			ref := n.Path
			if config.Source() == config.SourceApple {
				ref = n.ID
			}
			return m, deleteNoteCmd(n.ID, ref)
		}
	case "S":
		m.sortByDate = !m.sortByDate
		m = m.applySortOrder()
		if m.sortByDate {
			m.setStatus("Sort: date")
		} else {
			m.setStatus("Sort: title A–Z")
		}
	case "y":
		if len(m.notes) > 0 {
			m.setStatus("Copied: " + runeLimit(m.notes[m.cursor].Title, 30))
			return m, copyToClipboardCmd(m.notes[m.cursor].Title)
		}
	case "<":
		if m.isTwoPane() && m.paneRatio > 0.2 {
			m.paneRatio -= 0.05
		}
	case ">":
		if m.isTwoPane() && m.paneRatio < 0.65 {
			m.paneRatio += 0.05
		}
	case "p":
		m.vaultInput.SetValue(config.VaultPathRaw())
		m.vaultInput.Focus()
		m.view = viewSettings
		return m, nil
	case "s":
		if !m.syncing {
			m.syncing = true
			m.setStatus("Syncing…")
			return m, tea.Batch(doSyncCmd(), m.sp.Tick)
		}
	case "/":
		m.searching = true
		m.searchInput.Focus()
		m.searchInput.SetValue("")
	case "esc":
		if m.confirmID != "" {
			m.confirmID = ""
			m.status = ""
			return m, nil
		}
		if m.searchQ != "" {
			m.searchQ = ""
			m.searchInput.SetValue("")
			m.cursor = 0
			return m, loadNotesCmd("", m.activeFolder())
		}
	}

	// refresh preview when cursor moved
	if m.cursor != prevCursor {
		var cmd tea.Cmd
		m, cmd = m.refreshPreview()
		extraCmd = cmd
	}
	return m, extraCmd
}

// refreshPreview updates the two-pane preview viewport for the current cursor note.
func (m Model) refreshPreview() (Model, tea.Cmd) {
	if !m.isTwoPane() || len(m.notes) == 0 {
		return m, nil
	}
	n := m.notes[m.cursor]
	if n.Body != "" {
		setChecklistStateFor(n.ID)
		m.pvp.SetContent(renderMarkdown(n.Body, m.pvpWidth()))
		m.pvp.GotoTop()
		return m, nil
	}
	if config.Source() == config.SourceApple {
		m.pvp.SetContent(styleMuted.Render("Loading…"))
		m.pvp.GotoTop()
		return m, loadAppleBodyCmd(n.ID)
	}
	m.pvp.SetContent("")
	return m, nil
}

func (m Model) updateDetail(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "esc":
		m.view = viewList
		m.detail = nil
		m.detailLineCursor = 0
		m.detailYOffset = 0
		return m, nil

	case "e":
		if m.detail != nil {
			m.editNote = m.detail
			m.resetNew(m.detail.Title)
			m.titleInput.SetValue(m.detail.Title)
			m.tagsInput.SetValue(strings.Join(m.detail.Tags, ", "))
			m.bodyArea.SetValue(m.detail.Body)
			m.view = viewNew
			return m, nil
		}

	case "o":
		if m.detail != nil {
			return m, openExternalCmd(m.detail.ID, m.detail.Title, m.detail.Path)
		}

	case "d":
		if m.detail != nil {
			ref := m.detail.Path
			if config.Source() == config.SourceApple {
				ref = m.detail.ID
			}
			id, path, title := m.detail.ID, ref, m.detail.Title
			for i := range m.notes {
				if m.notes[i].ID == id {
					m.notes = append(m.notes[:i], m.notes[i+1:]...)
					if m.cursor >= len(m.notes) {
						m.cursor = max(0, len(m.notes)-1)
					}
					break
				}
			}
			m.detail = nil
			m.detailLineCursor = 0
			m.detailYOffset = 0
			m.view = viewList
			m.setStatus("Deleted: " + title)
			return m, deleteNoteCmd(id, path)
		}

	case "j", "down":
		if m.detail != nil {
			lines := strings.Split(m.detail.Body, "\n")
			if next := nextNonBlankLine(lines, m.detailLineCursor, 1); next != m.detailLineCursor {
				m.detailLineCursor = next
				content, visualCursor := renderDetailBody(m.detail.Body, m.detailLineCursor, m.detailBodyWidth())
				if visualCursor >= m.detailYOffset+m.vp.Height {
					m.detailYOffset = visualCursor - m.vp.Height + 1
				}
				m.vp.SetContent(content)
				m.vp.SetYOffset(m.detailYOffset)
			}
		}

	case "k", "up":
		if m.detail != nil {
			lines := strings.Split(m.detail.Body, "\n")
			if next := nextNonBlankLine(lines, m.detailLineCursor, -1); next != m.detailLineCursor {
				m.detailLineCursor = next
				content, visualCursor := renderDetailBody(m.detail.Body, m.detailLineCursor, m.detailBodyWidth())
				if visualCursor < m.detailYOffset {
					m.detailYOffset = visualCursor
				}
				m.vp.SetContent(content)
				m.vp.SetYOffset(m.detailYOffset)
			}
		}

	case "pgdown", "ctrl+f":
		if m.detail != nil {
			lines := strings.Split(m.detail.Body, "\n")
			m.detailLineCursor = min(len(lines)-1, m.detailLineCursor+max(1, m.vp.Height/2))
			m = m.syncDetailViewport()
		}

	case "pgup", "ctrl+b":
		if m.detail != nil {
			m.detailLineCursor = max(0, m.detailLineCursor-max(1, m.vp.Height/2))
			m = m.syncDetailViewport()
		}

	case " ":
		// toggle ☐ ↔ ☑ on current line, write back to Apple Notes
		if m.detail != nil {
			lines := strings.Split(m.detail.Body, "\n")
			if m.detailLineCursor < len(lines) {
				toggled := toggleCheckboxLine(lines[m.detailLineCursor])
				if toggled != lines[m.detailLineCursor] {
					lines[m.detailLineCursor] = toggled
					newBody := strings.Join(lines, "\n")
					m.detail.Body = newBody
					for i := range m.notes {
						if m.notes[i].ID == m.detail.ID {
							m.notes[i].Body = newBody
							break
						}
					}
					m = m.syncDetailViewport()
					if config.Source() == config.SourceApple {
						return m, saveAppleBodyCmd(m.detail.ID, newBody, m.detailBlocks)
					}
				}
			}
		}
	}
	return m, nil
}

// syncDetailViewport re-renders the detail viewport content and scrolls to keep
// detailLineCursor visible.
func (m Model) syncDetailViewport() Model {
	if m.detail == nil {
		m.detailYOffset = 0
		return m
	}
	content, visualCursor := renderDetailBody(m.detail.Body, m.detailLineCursor, m.detailBodyWidth())
	
	if visualCursor < m.detailYOffset {
		m.detailYOffset = visualCursor
	} else if visualCursor >= m.detailYOffset+m.vp.Height {
		m.detailYOffset = visualCursor - m.vp.Height + 1
	}
	if m.detailYOffset < 0 {
		m.detailYOffset = 0
	}
	
	m.vp.SetContent(content)
	m.vp.SetYOffset(m.detailYOffset)
	return m
}

func (m Model) updateNew(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+s":
		var id string
		var editBlocks []notes.Block
		if m.editNote != nil {
			id = m.editNote.ID
			editBlocks = m.editBlocks
		}
		return m, writeNoteCmd(
			id,
			m.titleInput.Value(),
			m.bodyArea.Value(),
			m.tagsInput.Value(),
			m.activeFolder(),
			editBlocks,
		)
	case "esc":
		m.view = viewList
		return m, nil
	case "tab":
		if m.newFocus < 2 {
			m.blurNew(m.newFocus)
			m.newFocus++
			m.focusNew(m.newFocus)
		}
		return m, nil
	case "shift+tab":
		if m.newFocus > 0 {
			m.blurNew(m.newFocus)
			m.newFocus--
			m.focusNew(m.newFocus)
		}
		return m, nil
	}
	var cmd tea.Cmd
	switch m.newFocus {
	case 0:
		m.titleInput, cmd = m.titleInput.Update(msg)
	case 1:
		m.tagsInput, cmd = m.tagsInput.Update(msg)
	case 2:
		m.bodyArea, cmd = m.bodyArea.Update(msg)
		m.syncEditorScroll()
	}
	return m, cmd
}

func (m Model) updateSettings(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+s":
		return m, saveSettingsCmd(m.vaultInput.Value(), sourceTypes[m.sourceIdx].key)
	case "esc":
		m.view = viewList
		return m, nil
	case "left", "h":
		if m.sourceIdx > 0 {
			m.sourceIdx--
		}
		return m, nil
	case "right", "l":
		if m.sourceIdx < len(sourceTypes)-1 {
			m.sourceIdx++
		}
		return m, nil
	}
	var cmd tea.Cmd
	m.vaultInput, cmd = m.vaultInput.Update(msg)
	return m, cmd
}

// ── Detail body helpers ───────────────────────────────────────────────────────

// currentChecklistState holds the real checked/unchecked state for whichever
// Apple note is currently displayed (detail view or list preview), keyed by
// each checklist item's trimmed text — see notes.ChecklistState for why this
// can't just be parsed out of the note body like everything else. It's
// package-level rather than threaded through render call args because
// renderDetailBody/renderMarkdown/renderMDLine are plain functions called
// from many places in Update/View; Bubble Tea's single-threaded Update/View
// loop means there's no concurrent-render hazard in setting it right before
// a render pass. nil means "unknown, or genuinely no Apple checklist items
// here" — either way, bullets render as plain bullets rather than a guessed
// checkbox, which is the whole point of having this at all.
var currentChecklistState map[string]bool

// checklistLookup reports whether a bullet's text is a real Apple checklist
// item and, if so, its done state. isItem is false for anything not
// confirmed as a checklist item (a plain bullet, or state not yet loaded) —
// callers should render those as plain bullets, not a checkbox.
func checklistLookup(text string) (isItem, done bool) {
	if currentChecklistState == nil {
		return false, false
	}
	d, ok := currentChecklistState[strings.TrimSpace(text)]
	return ok, d
}

// setChecklistStateFor loads the real checklist state for an Apple note
// (best-effort — see notes.ChecklistState) into currentChecklistState ahead
// of a render pass. Call this with the ID of whichever note is about to be
// rendered; on any failure (no Full Disk Access, note not found, non-Apple
// source) it clears the state so bullets fall back to plain rendering
// instead of showing stale state from a previously viewed note.
func setChecklistStateFor(id string) {
	if config.Source() != config.SourceApple || id == "" {
		currentChecklistState = nil
		return
	}
	state, err := notes.ChecklistState(id)
	if err != nil {
		currentChecklistState = nil
		return
	}
	currentChecklistState = state
}

// nextNonBlankLine walks from `from` in direction dir (+1 or -1), skipping
// blank/whitespace-only lines, and returns the index of the next non-blank
// line. It returns `from` unchanged if there is no non-blank line before the
// start/end of lines in that direction, so j/k navigation stops instead of
// landing on empty space between list items or paragraphs.
func nextNonBlankLine(lines []string, from, dir int) int {
	i := from + dir
	for i >= 0 && i < len(lines) {
		if strings.TrimSpace(lines[i]) != "" {
			return i
		}
		i += dir
	}
	return from
}

// renderDetailBody renders the note body with line-level cursor highlighting.
// Checkbox lines get ☐/☑ preserved; the selected line is highlighted.
func renderDetailBody(body string, cursor, width int) (string, int) {
	if body == "" {
		return styleMuted.Render("(empty)"), 0
	}
	lines := strings.Split(body, "\n")
	lines = preprocessMarkdownTables(lines, width)

	var sb strings.Builder
	visualCursor := 0
	currentVisualLines := 0

	for i, line := range lines {
		disp := line
		trimmedDisp := strings.TrimSpace(disp)
		if config.Source() == config.SourceApple {
			idx := strings.IndexFunc(disp, func(r rune) bool { return r != ' ' && r != '\t' })
			leading := ""
			if idx > 0 {
				leading = disp[:idx]
			}
			for _, pfx := range []string{"• ", "- ", "* "} {
				if !strings.HasPrefix(trimmedDisp, pfx) {
					continue
				}
				itemText := strings.TrimPrefix(trimmedDisp, pfx)
				if isItem, done := checklistLookup(itemText); isItem {
					marker := "☐ "
					if done {
						marker = "☑ "
					}
					disp = leading + marker + itemText
					trimmedDisp = strings.TrimSpace(disp)
				}
				break
			}
		}

		var formatted string
		if i == cursor {
			formatted = styleSelected.Render(renderMDLine(disp, width))
			visualCursor = currentVisualLines
		} else if strings.HasPrefix(trimmedDisp, "☑ ") || strings.HasPrefix(trimmedDisp, "- [x] ") || strings.HasPrefix(trimmedDisp, "- [X] ") || strings.HasPrefix(trimmedDisp, "* [x] ") || strings.HasPrefix(trimmedDisp, "* [X] ") {
			text := trimmedDisp
			for _, pfx := range []string{"☑ ", "- [x] ", "- [X] ", "* [x] ", "* [X] "} {
				if strings.HasPrefix(text, pfx) {
					text = strings.TrimPrefix(text, pfx)
					break
				}
			}
			idx := strings.IndexFunc(disp, func(r rune) bool { return r != ' ' && r != '\t' })
			leading := ""
			if idx > 0 {
				leading = disp[:idx]
			}
			formatted = leading + styleStrike.Render("☑ " + renderInline(text))
		} else if strings.HasPrefix(trimmedDisp, "☐ ") || strings.HasPrefix(trimmedDisp, "- [ ] ") || strings.HasPrefix(trimmedDisp, "* [ ] ") {
			text := trimmedDisp
			for _, pfx := range []string{"☐ ", "- [ ] ", "* [ ] "} {
				if strings.HasPrefix(text, pfx) {
					text = strings.TrimPrefix(text, pfx)
					break
				}
			}
			idx := strings.IndexFunc(disp, func(r rune) bool { return r != ' ' && r != '\t' })
			leading := ""
			if idx > 0 {
				leading = disp[:idx]
			}
			formatted = leading + styleMuted.Render("☐ " + renderInline(text))
		} else {
			formatted = renderMDLine(disp, width)
		}

		// Count visual lines this logical line will take when wrapped
		// wrap.String wraps at the given width, we split by \n to count
		wrapped := wrap.String(formatted, width)
		currentVisualLines += strings.Count(wrapped, "\n") + 1
		
		sb.WriteString(wrapped + "\n")
	}
	return sb.String(), visualCursor
}

// toggleCheckboxLine toggles list items and checkboxes.
// • item  →  ☑ item  (check off a regular bullet)
// ☑ item  →  • item / ☐ item  (uncheck back to bullet or box)
// ☐ item  →  ☑ item  (check an Apple-checklist item)
func toggleCheckboxLine(line string) string {
	idx := strings.IndexFunc(line, func(r rune) bool { return r != ' ' && r != '\t' })
	leading := ""
	trimmed := line
	if idx > 0 {
		leading = line[:idx]
		trimmed = line[idx:]
	}
	switch {
	case strings.HasPrefix(trimmed, "- [ ] "):
		return leading + "- [x] " + strings.TrimPrefix(trimmed, "- [ ] ")
	case strings.HasPrefix(trimmed, "* [ ] "):
		return leading + "* [x] " + strings.TrimPrefix(trimmed, "* [ ] ")
	case strings.HasPrefix(trimmed, "- [x] ") || strings.HasPrefix(trimmed, "- [X] "):
		return leading + "- [ ] " + trimmed[6:]
	case strings.HasPrefix(trimmed, "* [x] ") || strings.HasPrefix(trimmed, "* [X] "):
		return leading + "* [ ] " + trimmed[6:]
	case strings.HasPrefix(trimmed, "• "):
		return leading + "☑ " + strings.TrimPrefix(trimmed, "• ")
	case strings.HasPrefix(trimmed, "- "):
		return leading + "☑ " + strings.TrimPrefix(trimmed, "- ")
	case strings.HasPrefix(trimmed, "* "):
		return leading + "☑ " + strings.TrimPrefix(trimmed, "* ")
	case strings.HasPrefix(trimmed, "☑ "):
		return leading + "☐ " + strings.TrimPrefix(trimmed, "☑ ")
	case strings.HasPrefix(trimmed, "☐ "):
		return leading + "☑ " + strings.TrimPrefix(trimmed, "☐ ")
	}
	return line
}

// saveAppleBodyCmd converts the text body (with ☐/☑) back to HTML and writes
// it to the Apple Notes note with the given id, using block reconciliation to preserve formatting.
func saveAppleBodyCmd(id, textBody string, detailBlocks []notes.Block) tea.Cmd {
	return func() tea.Msg {
		html := notes.ReconcileBlocks(detailBlocks, textBody)
		if err := notes.UpdateBody(id, html); err != nil {
			return errMsg{err}
		}
		body, err := notes.ReadApple(id)
		return appleBodyMsg{id: id, body: body, err: err}
	}
}

// ── View ──────────────────────────────────────────────────────────────────────

func (m Model) View() string {
	switch m.view {
	case viewDetail:
		return m.renderDetail()
	case viewNew:
		return m.renderNew()
	case viewSettings:
		return m.renderSettings()
	default:
		return m.renderList()
	}
}

func (m Model) renderList() string {
	if m.isTwoPane() {
		return m.renderTwoPane()
	}
	return m.renderSinglePane()
}

// ── Single-pane (narrow terminals) ────────────────────────────────────────────

func (m Model) renderSinglePane() string {
	var b strings.Builder
	b.WriteString(" " + m.renderAppHeader(m.width-1) + "\n")
	b.WriteString(" " + m.renderTabBar(m.width-1) + "\n")
	b.WriteString(styleDivider.Render(strings.Repeat("─", m.width)) + "\n")

	overhead := 4 // header + tabbar + divider + helpbar
	if m.searching {
		b.WriteString("  " + m.searchInput.View() + "\n\n")
		overhead += 2
	}
	if m.searchQ != "" {
		b.WriteString(styleMuted.Render("  /"+m.searchQ) + "\n")
		overhead++
	}

	listH := m.height - overhead
	if listH < 1 {
		listH = 1
	}

	if len(m.notes) == 0 {
		b.WriteString("\n" + styleHelp.Render("  "+emptyHint()) + "\n")
	} else {
		lines, cursorLine := m.buildListLines(m.width, true)
		start := 0
		if cursorLine >= listH {
			start = cursorLine - listH + 1
		}
		end := min(len(lines), start+listH)
		for _, l := range lines[start:end] {
			b.WriteString(l + "\n")
		}
	}

	b.WriteString("\n" + m.renderHelpBar(m.width))
	return b.String()
}

// ── Two-pane (wide terminals) ─────────────────────────────────────────────────

func (m Model) renderTwoPane() string {
	leftW := m.leftWidth()
	rightW := m.pvpWidth()
	paneH := m.height - 4 // header(1) + tab(1) + divider(1) + helpbar(1)

	var b strings.Builder
	b.WriteString(" " + m.renderAppHeader(m.width-1) + "\n")
	b.WriteString(" " + m.renderTabBar(m.width-1) + "\n")
	b.WriteString(styleDivider.Render(strings.Repeat("─", m.width)) + "\n")

	// search row replaces one line of the pane
	if m.searching {
		b.WriteString("  " + m.searchInput.View() + "\n")
		paneH--
	}

	// Reserve a 1-column margin on each side of the divider (left pane's
	// own left edge, both sides of the "│", and the right pane's right
	// edge implicitly via its narrower content width) so list rows and
	// preview text don't render flush against the pane borders.
	const pad = 1
	listContentW := max(1, leftW-pad*2)
	rightContentW := max(1, rightW-pad)

	// ── left: note list ──
	var leftLines []string
	if len(m.notes) == 0 {
		leftLines = []string{styleHelp.Render(" " + emptyHint())}
	} else {
		lines, cursorLine := m.buildListLines(listContentW, false)
		start := 0
		if cursorLine >= paneH {
			start = cursorLine - paneH + 1
		}
		end := min(len(lines), start+paneH)
		leftLines = lines[start:end]
	}

	// ── right: markdown preview ──
	var rightLines []string
	if len(m.notes) > 0 {
		body := m.notes[m.cursor].Body
		if body == "" && config.Source() != config.SourceApple {
			body = ""
		}
		rendered := renderMarkdown(body, rightContentW)
		rightLines = strings.Split(rendered, "\n")
	}

	// combine side by side
	div := styleDivider.Render("│")
	for i := 0; i < paneH; i++ {
		l := ""
		if i < len(leftLines) {
			l = leftLines[i]
		}
		r := ""
		if i < len(rightLines) {
			r = rightLines[i]
		}
		lW := lipgloss.Width(l)
		if lW < listContentW {
			l += strings.Repeat(" ", listContentW-lW)
		}
		b.WriteString(" " + l + " " + div + " " + r + "\n")
	}

	b.WriteString(m.renderHelpBar(m.width))
	return b.String()
}

// buildListLines pre-renders list rows with optional date group headers and preview lines.
func (m Model) buildListLines(w int, withPreview bool) ([]string, int) {
	var lines []string
	cursorLine := 0
	lastGroup := ""

	for i := range m.notes {
		n := &m.notes[i]

		// date group header (only when sorted by date)
		if m.sortByDate {
			g := dateGroup(n.ModTime)
			if g != lastGroup {
				if len(lines) > 0 {
					lines = append(lines, "") // blank separator
				}
				lines = append(lines, renderGroupHeader(g, w))
				lastGroup = g
			}
		}

		if i == m.cursor {
			cursorLine = len(lines)
		}
		row := formatNoteRow(n, w)
		if i == m.cursor {
			row = styleSelected.Width(w).Render(row)
		}
		lines = append(lines, row)

		if withPreview && n.Body != "" {
			preview := firstBodyLine(n.Body)
			if preview != "" {
				avail := w - 16
				if avail > 10 {
					preview = runewidth.Truncate(preview, avail, "…")
				}
				pLine := strings.Repeat(" ", 16) + preview
				if i == m.cursor {
					pLine = styleSelected.Width(w).Render(pLine)
				} else {
					pLine = styleMuted.Render(pLine)
				}
				lines = append(lines, pLine)
			}
		}
	}
	return lines, cursorLine
}

func (m Model) renderAppHeader(w int) string {
	left := styleHeader.Render("notectl")
	right := styleMuted.Render(time.Now().Format("Mon, 02 Jan 2006"))
	pad := w - lipgloss.Width(left) - lipgloss.Width(right)
	if pad < 1 {
		pad = 1
	}
	return left + strings.Repeat(" ", pad) + right
}

func (m Model) renderTabBar(w int) string {
	tabs := append([]string{"All"}, m.folders...)
	var parts []string
	for i, t := range tabs {
		label := t
		folderKey := t
		if i == 0 {
			folderKey = "" // "All" → total count
		}
		if c := m.folderCounts[folderKey]; c > 0 {
			label = fmt.Sprintf("%s %d", t, c)
		}
		if i == m.activeTab {
			parts = append(parts, styleTabActive.Render(label))
		} else {
			parts = append(parts, styleTabInact.Render(label))
		}
	}
	bar := strings.Join(parts, "  ")
	if m.syncing {
		bar += "  " + m.sp.View() + styleSyncing.Render(" syncing…")
	}
	_ = w
	return bar
}

func (m Model) renderHelpBar(w int) string {
	right := ""
	if len(m.notes) > 0 {
		sortIcon := "↓date"
		if !m.sortByDate {
			sortIcon = "↓A-Z"
		}
		right = styleHelp.Render(fmt.Sprintf("%d/%d  %s", m.cursor+1, len(m.notes), sortIcon))
	}
	var helpBar string
	if m.err != nil {
		helpBar = styleErr.Render("✗ " + m.err.Error())
	} else if m.status != "" {
		if m.confirmID != "" {
			helpBar = styleSyncing.Render("⚠ " + m.status)
		} else {
			helpBar = styleOK.Render("✓ " + m.status)
		}
	} else {
		helpBar = styleHelp.Render("enter:open  n:new  e:edit  d:delete  y:copy  S:sort  o:editor  s:sync  p:settings  /:search  tab:folder  q:quit")
	}
	pad := w - lipgloss.Width(helpBar) - lipgloss.Width(right)
	if pad < 0 {
		pad = 0
	}
	return helpBar + strings.Repeat(" ", pad) + right
}

func (m Model) renderDetail() string {
	if m.detail == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(detailLeftPad + styleBold.Render(m.detail.Title) + "\n")
	meta := ""
	if m.detail.Folder != "" {
		meta += styleFolder.Render(m.detail.Folder) + "  "
	}
	for _, t := range m.detail.Tags {
		meta += styleTag.Render("#"+t) + " "
	}
	if meta != "" {
		b.WriteString(detailLeftPad + meta + "\n")
	}
	b.WriteString(detailLeftPad + styleMuted.Render(m.detail.ModTime.Format("Mon, 02 Jan 2006 15:04")) + "\n\n")
	b.WriteString(styleDivider.Render(strings.Repeat("─", m.width)) + "\n")
	m.vp.Width = m.detailBodyWidth()
	m.vp.Height = m.bodyHeight()
	b.WriteString(renderScrollbar(m.vp, detailLeftPad))
	pct := ""
	if m.vp.TotalLineCount() > m.vp.Height {
		pct = fmt.Sprintf(" %d%%", int(m.vp.ScrollPercent()*100))
	}
	helpStr := "esc:back  e:edit  d:delete  o:notes  j/k:scroll  space:toggle checkbox  q:quit"
	b.WriteString("\n\n" + detailLeftPad + styleHelp.Render(helpStr) + styleMuted.Render(pct))
	return b.String()
}

func renderScrollbar(vp viewport.Model, leftPad string) string {
	content := vp.View()
	lines := strings.Split(content, "\n")
	h := vp.Height
	if h <= 0 {
		h = len(lines)
	}
	total := vp.TotalLineCount()
	if total <= h {
		var sb strings.Builder
		for _, l := range lines {
			sb.WriteString(leftPad + l + "\n")
		}
		return strings.TrimRight(sb.String(), "\n")
	}
	thumbH := max(1, h*h/total)
	thumbTop := int(vp.ScrollPercent() * float64(h-thumbH))
	track := styleDivider.Render("│")
	// A heavy line rather than a full block ("█") — same single-column
	// width as the track, just a bolder stroke, so the thumb reads as a
	// slim scroll indicator instead of a chunky rectangle bulging out of
	// an otherwise thin line.
	thumb := lipgloss.NewStyle().Foreground(colorBlue).Render("┃")
	var glyphs strings.Builder
	for i := range lines {
		if i > 0 {
			glyphs.WriteByte('\n')
		}
		if i >= thumbTop && i < thumbTop+thumbH {
			glyphs.WriteString(thumb)
		} else {
			glyphs.WriteString(track)
		}
	}
	// Content lines are only as wide as their own wrapped text (viewport
	// content isn't right-padded), so appending the glyph column after a
	// manually-padded string was fragile: it needs a width measurement that
	// exactly matches how each line was wrapped, and at least one real
	// emoji in practice ("🛏️", bed + variation selector) gets measured
	// differently by different width functions, throwing just that line's
	// glyph out of column. JoinHorizontal sidesteps the whole problem: it
	// pads the left block to a uniform width using its own single,
	// consistent measurement before attaching the right block, so the
	// glyph column can't drift regardless of what any individual line
	// contains.
	body := lipgloss.JoinHorizontal(lipgloss.Top, content, " "+glyphs.String())
	var sb strings.Builder
	for _, l := range strings.Split(body, "\n") {
		sb.WriteString(leftPad + l + "\n")
	}
	return strings.TrimRight(sb.String(), "\n")
}

func (m Model) renderNew() string {
	title := "New Note"
	if m.editNote != nil {
		title = "Edit: " + m.editNote.Title
	}
	leftW := m.width
	if m.isTwoPane() {
		leftW = m.leftWidth()
	}

	var b strings.Builder
	b.WriteString(styleHeader.Render(title) + "\n")
	b.WriteString(styleDivider.Render(strings.Repeat("─", leftW)) + "\n\n")

	focus := func(i int) string {
		if m.newFocus == i {
			return styleTabActive.Render("›")
		}
		return "  "
	}

	b.WriteString(focus(0) + " " + styleLabel.Render("Title:") + "  " + m.titleInput.View() + "\n")
	b.WriteString(focus(1) + " " + styleLabel.Render("Tags:") + "   " + m.tagsInput.View() + "\n\n")
	b.WriteString(focus(2) + " " + styleLabel.Render("Body:") + "\n")
	b.WriteString(m.bodyArea.View() + "\n")
	b.WriteString(styleMuted.Render("  # heading  - list  - [ ] checklist  **bold**  *italic*  ~~strike~~  `code`") + "\n\n")

	if m.err != nil {
		b.WriteString(styleErr.Render("✗ " + m.err.Error()))
	} else {
		b.WriteString(styleHelp.Render("tab:next  ctrl+s:save  esc:cancel"))
	}
	if !m.isTwoPane() {
		return b.String()
	}

	// ── live preview pane (wide terminals) ──
	rightW := m.pvpWidth()
	rightLines := []string{styleMuted.Render(" Preview"), ""}
	rightLines = append(rightLines, strings.Split(renderMarkdown(m.bodyArea.Value(), rightW-1), "\n")...)
	leftLines := strings.Split(b.String(), "\n")
	div := styleDivider.Render("│")
	rows := max(len(leftLines), len(rightLines))
	if rows > m.height {
		rows = m.height
	}
	var out strings.Builder
	for i := 0; i < rows; i++ {
		l := ""
		if i < len(leftLines) {
			l = leftLines[i]
		}
		r := ""
		if i < len(rightLines) {
			r = " " + rightLines[i]
		}
		if lW := lipgloss.Width(l); lW < leftW {
			l += strings.Repeat(" ", leftW-lW)
		}
		out.WriteString(l + div + r + "\n")
	}
	return strings.TrimRight(out.String(), "\n")
}

func (m Model) renderSettings() string {
	w := min(m.width, 100)
	var b strings.Builder

	b.WriteString(styleHeader.Render("notectl") + "  " + styleMuted.Render("Settings") + "\n")
	b.WriteString(styleDivider.Render(strings.Repeat("─", w)) + "\n\n")

	b.WriteString(styleLabel.Render("Vault:") + "\n")
	b.WriteString("  " + m.vaultInput.View() + "\n")
	if strings.HasPrefix(m.vaultInput.Value(), "~") {
		resolved := config.VaultPath()
		if _, err := filepath.Abs(resolved); err == nil {
			b.WriteString(styleMuted.Render("  → "+resolved) + "\n")
		}
	}
	b.WriteString("\n")

	b.WriteString(styleLabel.Render("Source:") + "\n  ")
	for i, s := range sourceTypes {
		if i == m.sourceIdx {
			b.WriteString(styleTabActive.Render(s.label))
		} else {
			b.WriteString(styleTabInact.Render(s.label))
		}
		if i < len(sourceTypes)-1 {
			b.WriteString("  ")
		}
	}
	b.WriteString("\n")
	b.WriteString("  " + styleMuted.Render(sourceTypes[m.sourceIdx].note) + "\n\n")

	if m.err != nil {
		b.WriteString(styleErr.Render("✗ "+m.err.Error()) + "\n")
	} else if m.status != "" {
		b.WriteString(styleOK.Render("✓ "+m.status) + "\n")
	}

	b.WriteString("\n" + styleHelp.Render("←/→:source  ctrl+s:save  esc:cancel"))
	return b.String()
}

// ── Markdown renderer ─────────────────────────────────────────────────────────

func renderMarkdown(body string, width int) string {
	if body == "" {
		return styleMuted.Render("(empty)")
	}
	lines := strings.Split(body, "\n")
	lines = preprocessMarkdownTables(lines, width)
	var sb strings.Builder
	for _, line := range lines {
		sb.WriteString(renderMDLine(line, width) + "\n")
	}
	return lipgloss.NewStyle().Width(width).Render(sb.String())
}

func renderMDLine(line string, width int) string {
	t := strings.TrimSpace(line)
	idx := strings.IndexFunc(line, func(r rune) bool { return r != ' ' && r != '\t' })
	leading := ""
	if idx > 0 {
		leading = line[:idx]
	}

	switch {
	case strings.HasPrefix(t, "### "):
		return leading + styleMDH3.Render(strings.TrimPrefix(t, "### "))
	case strings.HasPrefix(t, "## "):
		return leading + styleMDH2.Render(strings.TrimPrefix(t, "## "))
	case strings.HasPrefix(t, "# "):
		return leading + styleMDH1.Render(strings.TrimPrefix(t, "# "))
	case strings.HasPrefix(t, "> "):
		return leading + styleMDQuote.Render("│ " + renderInline(strings.TrimPrefix(t, "> ")))
	case t == ">":
		return leading + styleMDQuote.Render("│")
	case t == "---" || t == "***" || t == "___":
		return styleDivider.Render(strings.Repeat("─", width))
	case strings.HasPrefix(t, "├"):
		return leading + styleMuted.Render(t)
	case strings.HasPrefix(t, "│"):
		var sb strings.Builder
		sb.WriteString(leading)
		parts := strings.Split(t, "│")
		for j, p := range parts {
			if j > 0 {
				sb.WriteString(styleMuted.Render("│"))
			}
			sb.WriteString(renderInline(p))
		}
		return sb.String()
	case strings.HasPrefix(t, "- [ ] ") || strings.HasPrefix(t, "* [ ] "):
		return leading + styleMuted.Render("☐ ") + renderInline(t[6:])
	case strings.HasPrefix(t, "- [x] ") || strings.HasPrefix(t, "- [X] ") ||
		strings.HasPrefix(t, "* [x] ") || strings.HasPrefix(t, "* [X] "):
		return leading + styleStrike.Render("☑ " + renderInline(t[6:]))
	case config.Source() == config.SourceApple && (strings.HasPrefix(t, "• ") || strings.HasPrefix(t, "- ") || strings.HasPrefix(t, "* ")):
		text := t
		if strings.HasPrefix(t, "• ") {
			text = strings.TrimPrefix(t, "• ")
		} else {
			text = t[2:]
		}
		if isItem, done := checklistLookup(text); isItem {
			if done {
				return leading + styleStrike.Render("☑ " + renderInline(text))
			}
			return leading + styleMuted.Render("☐ ") + renderInline(text)
		}
		return leading + "  • " + renderInline(text)
	case strings.HasPrefix(t, "- ") || strings.HasPrefix(t, "* "):
		return leading + "  • " + renderInline(t[2:])
	case strings.HasPrefix(t, "• "):
		return leading + "  " + renderInline(t)
	case strings.HasPrefix(t, "☑ "):
		return leading + styleStrike.Render("☑ " + renderInline(strings.TrimPrefix(t, "☑ ")))
	case strings.HasPrefix(t, "☐ "):
		return leading + styleMuted.Render("☐ ") + renderInline(strings.TrimPrefix(t, "☐ "))
	case strings.HasPrefix(t, "```"):
		return styleMDCode.Render(t)
	default:
		return renderInline(line)
	}
}

func renderInline(s string) string {
	var out strings.Builder
	for i := 0; i < len(s); {
		// **bold**
		if strings.HasPrefix(s[i:], "**") {
			if end := strings.Index(s[i+2:], "**"); end >= 0 {
				leading, content, trailing := splitLeadingTrailingSpaces(s[i+2 : i+2+end])
				out.WriteString(leading + styleMDBold.Render(content) + trailing)
				i += 2 + end + 2
				continue
			}
		}
		// ~~strikethrough~~
		if strings.HasPrefix(s[i:], "~~") {
			if end := strings.Index(s[i+2:], "~~"); end >= 0 {
				leading, content, trailing := splitLeadingTrailingSpaces(s[i+2 : i+2+end])
				out.WriteString(leading + styleStrike.Render(content) + trailing)
				i += 2 + end + 2
				continue
			}
		}
		// *italic*
		if s[i] == '*' && (i == 0 || s[i-1] != '*') && (i+1 >= len(s) || s[i+1] != '*') {
			if end := strings.Index(s[i+1:], "*"); end >= 0 && !strings.HasPrefix(s[i+1+end:], "**") {
				leading, content, trailing := splitLeadingTrailingSpaces(s[i+1 : i+1+end])
				out.WriteString(leading + styleMuted.Render(content) + trailing)
				i += 1 + end + 1
				continue
			}
		}
		// `code`
		if s[i] == '`' {
			if end := strings.Index(s[i+1:], "`"); end >= 0 {
				leading, content, trailing := splitLeadingTrailingSpaces(s[i+1 : i+1+end])
				out.WriteString(leading + styleMDCode.Render(content) + trailing)
				i += 1 + end + 1
				continue
			}
		}
		out.WriteByte(s[i])
		i++
	}
	return out.String()
}

func splitLeadingTrailingSpaces(s string) (leading, content, trailing string) {
	start := 0
	for start < len(s) && (s[start] == ' ' || s[start] == '\t' || s[start] == '\n' || s[start] == '\r') {
		start++
	}
	if start == len(s) {
		return s, "", ""
	}
	end := len(s)
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\n' || s[end-1] == '\r') {
		end--
	}
	return s[:start], s[start:end], s[end:]
}

func stripInlineMarkdownForWidth(s string) string {
	s = strings.ReplaceAll(s, "**", "")
	s = strings.ReplaceAll(s, "__", "")
	s = strings.ReplaceAll(s, "*", "")
	s = strings.ReplaceAll(s, "_", "")
	s = strings.ReplaceAll(s, "`", "")
	s = strings.ReplaceAll(s, "~~", "")
	return s
}

func preprocessMarkdownTables(lines []string, width int) []string {
	out := make([]string, len(lines))
	copy(out, lines)
	for i := 0; i < len(out); i++ {
		t := strings.TrimSpace(out[i])
		if strings.HasPrefix(t, "|") && strings.HasSuffix(t, "|") {
			end := i
			for end < len(out) {
				t2 := strings.TrimSpace(out[end])
				if !(strings.HasPrefix(t2, "|") && strings.HasSuffix(t2, "|")) {
					break
				}
				end++
			}
			if end-i >= 2 {
				tableLines := formatMarkdownTable(out[i:end], width)
				for j := i; j < end; j++ {
					if j-i < len(tableLines) {
						out[j] = tableLines[j-i]
					}
				}
			}
			i = end - 1
		}
	}
	return out
}

func formatMarkdownTable(lines []string, width ...int) []string {
	if len(lines) < 2 {
		return lines
	}
	wMax := 0
	if len(width) > 0 {
		wMax = width[0]
	}
	var rows [][]string
	for _, l := range lines {
		cells := strings.Split(strings.Trim(strings.TrimSpace(l), "|"), "|")
		for i := range cells {
			cells[i] = strings.TrimSpace(cells[i])
		}
		rows = append(rows, cells)
	}

	colWidths := make([]int, 0)
	for i, row := range rows {
		if i == 1 && len(row) > 0 && strings.HasPrefix(row[0], "-") {
			continue // skip separator
		}
		for j, cell := range row {
			w := runewidth.StringWidth(stripInlineMarkdownForWidth(cell))
			if j >= len(colWidths) {
				colWidths = append(colWidths, w)
			} else if w > colWidths[j] {
				colWidths[j] = w
			}
		}
	}

	if wMax > 0 && len(colWidths) > 0 {
		numCols := len(colWidths)
		borders := 3*numCols + 1
		avail := wMax - borders
		if avail < numCols*3 {
			avail = numCols * 3
		}
		sum := 0
		for _, w := range colWidths {
			sum += w
		}
		for sum > avail {
			maxIdx := -1
			maxW := -1
			minW := max(3, avail/numCols)
			for j, w := range colWidths {
				if w > minW && w > maxW {
					maxW = w
					maxIdx = j
				}
			}
			if maxIdx == -1 {
				break
			}
			colWidths[maxIdx]--
			sum--
		}
	}

	var out []string
	for i, row := range rows {
		var sb strings.Builder
		if i == 1 && len(row) > 0 && strings.HasPrefix(row[0], "-") {
			sb.WriteString("├")
			for j, w := range colWidths {
				sb.WriteString(strings.Repeat("─", w+2))
				if j < len(colWidths)-1 {
					sb.WriteString("┼")
				}
			}
			sb.WriteString("┤")
		} else {
			sb.WriteString("│ ")
			for j, cell := range row {
				w := 0
				if j < len(colWidths) {
					w = colWidths[j]
				}
				cleanCell := stripInlineMarkdownForWidth(cell)
				actualW := runewidth.StringWidth(cleanCell)
				if actualW > w && w > 0 {
					cell = runewidth.Truncate(cleanCell, w, "…")
					actualW = runewidth.StringWidth(stripInlineMarkdownForWidth(cell))
				}
				sb.WriteString(cell)
				if w > actualW {
					sb.WriteString(strings.Repeat(" ", w-actualW))
				}
				if j < len(row)-1 {
					sb.WriteString(" │ ")
				}
			}
			sb.WriteString(" │")
		}
		out = append(out, sb.String())
	}
	return out
}

// ── Commands ──────────────────────────────────────────────────────────────────

func loadNotesCmd(query, folder string) tea.Cmd {
	return func() tea.Msg {
		s, err := store.New(config.DBPath())
		if err != nil {
			return errMsg{err}
		}
		defer s.Close()
		ctx := context.Background()
		ns, err := s.List(ctx, store.Filter{Query: query, Folder: folder, Limit: 500})
		if err != nil {
			return errMsg{err}
		}
		folders, _ := s.ListFolders(ctx)
		counts, _ := s.CountByFolder(ctx)
		return notesLoadedMsg{notes: ns, folders: folders, folderCounts: counts}
	}
}

func doSyncCmd() tea.Cmd {
	return func() tea.Msg {
		src := config.Source()
		var ns []models.Note
		var err error
		var srcKey string

		switch src {
		case config.SourceApple:
			ns, err = notes.ListApple(config.AppleFolder())
			srcKey = "apple"
		default:
			ns, err = notes.List(config.VaultPath())
			srcKey = "obsidian"
		}
		if err != nil {
			return syncDoneMsg{err: err}
		}
		s, err := store.New(config.DBPath())
		if err != nil {
			return syncDoneMsg{err: err}
		}
		defer s.Close()
		ctx := context.Background()
		_ = s.DeleteBySource(ctx, srcKey)
		for i := range ns {
			_ = s.Upsert(ctx, &ns[i])
		}
		return syncDoneMsg{count: len(ns)}
	}
}

func saveSettingsCmd(vaultPath string, source config.SourceType) tea.Cmd {
	return func() tea.Msg {
		if err := config.Save(vaultPath, source); err != nil {
			return savedSettingsMsg{err}
		}
		return savedSettingsMsg{}
	}
}

func deleteNoteCmd(id, relPath string) tea.Cmd {
	return func() tea.Msg {
		s, err := store.New(config.DBPath())
		if err != nil {
			return deletedMsg{err}
		}
		defer s.Close()
		if err := s.Delete(context.Background(), id); err != nil {
			return deletedMsg{err}
		}
		if config.Source() == config.SourceApple {
			_ = notes.DeleteApple(id)
		} else if relPath != "" {
			_ = notes.Delete(config.VaultPath(), relPath)
		}
		return deletedMsg{}
	}
}

// openExternalCmd opens a note in its native app.
// For Apple Notes it uses AppleScript; for file-based vaults it uses `open`.
func openExternalCmd(id, title, relPath string) tea.Cmd {
	return func() tea.Msg {
		if config.Source() == config.SourceApple {
			_ = notes.OpenApple(id)
			return nil
		}
		if relPath == "" {
			return nil
		}
		_ = exec.Command("open", config.VaultPath()+"/"+relPath).Start()
		return nil
	}
}

func writeNoteCmd(id, title, body, tagsStr, folder string, editBlocks []notes.Block) tea.Cmd {
	return func() tea.Msg {
		if title == "" {
			return writeDoneMsg{err: fmt.Errorf("title required")}
		}
		var tags []string
		for _, t := range strings.Split(tagsStr, ",") {
			if t = strings.TrimSpace(t); t != "" {
				tags = append(tags, t)
			}
		}

		var n *models.Note
		var err error

		if config.Source() == config.SourceApple {
			fullBody := title
			if body != "" {
				fullBody = title + "\n\n" + body
			}
			htmlBody := notes.ReconcileBlocks(editBlocks, fullBody)
			var newID string
			newID, err = notes.WriteApple(id, title, htmlBody, folder)
			if err != nil {
				return writeDoneMsg{err: err}
			}
			n = &models.Note{
				ID: newID, Title: title, Body: body,
				Tags: tags, Folder: folder, Source: "apple",
				ModTime: time.Now(), Created: time.Now(),
			}
		} else {
			n, err = notes.Write(config.VaultPath(), title, body, tags, folder)
			if err != nil {
				return writeDoneMsg{err: err}
			}
		}

		if s, serr := store.New(config.DBPath()); serr == nil {
			defer s.Close()
			_ = s.Upsert(context.Background(), n)
		}
		return writeDoneMsg{note: n}
	}
}

func loadAppleBodyCmd(id string) tea.Cmd {
	return func() tea.Msg {
		body, err := notes.ReadApple(id)
		return appleBodyMsg{id: id, body: body, err: err}
	}
}

func loadAppleBodyForEditCmd(id string) tea.Cmd {
	return func() tea.Msg {
		body, err := notes.ReadApple(id)
		return appleBodyMsg{id: id, body: body, err: err, goEdit: true}
	}
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// splitTitleBlock treats an Apple note's first block as its title (Notes
// derives the displayed title from the body's first line, so the editor
// shows it as its own field rather than duplicating it atop the body text)
// and everything after it as the editable body.
func splitTitleBlock(blocks []notes.Block) (title, rest string) {
	if len(blocks) == 0 {
		return "", ""
	}
	return blocks[0].Plain, notes.BlocksToPlain(blocks[1:])
}

func (m *Model) resetNew(title string) {
	m.bodyArea.SetWidth(m.editorBodyWidth()) // paneRatio may have changed since last resize
	m.editorYOffset = 0
	m.titleInput.SetValue(title)
	m.tagsInput.SetValue("")
	m.bodyArea.SetValue("")
	m.newFocus = 0
	m.titleInput.Focus()
	m.tagsInput.Blur()
	m.bodyArea.Blur()
}

func (m *Model) blurNew(f int) {
	switch f {
	case 0:
		m.titleInput.Blur()
	case 1:
		m.tagsInput.Blur()
	case 2:
		m.bodyArea.Blur()
	}
}

func (m *Model) focusNew(f int) {
	switch f {
	case 0:
		m.titleInput.Focus()
	case 1:
		m.tagsInput.Focus()
	case 2:
		m.bodyArea.Focus()
	}
}

func (m *Model) setStatus(s string) {
	m.status = s
	m.statusTime = time.Now()
}

func (m Model) bodyHeight() int {
	h := m.height - 10
	if h < 5 {
		h = 5
	}
	return h
}

// detailBodyWidth is the wrap width for detail-view body content, leaving
// room for detailLeftPad on the left and the scrollbar glyph on the right
// (see renderDetail/renderScrollbar) so text doesn't run flush to either.
func (m Model) detailBodyWidth() int {
	w := m.width - 4
	if w < 10 {
		w = 10
	}
	return w
}

// detailLeftPad is the left margin applied to every line in the detail
// view (header fields and scrollable body alike).
const detailLeftPad = "  "

func formatNoteRow(n *models.Note, width int) string {
	dateStr := smartDate(n.ModTime)
	dateStyled := coloredDate(dateStr, n.ModTime)
	title := n.Title
	if idx := strings.Index(title, "\n"); idx >= 0 {
		title = title[:idx]
	}
	title = strings.TrimSpace(title)
	meta := ""
	if n.Folder != "" {
		meta += styleFolder.Render(" " + n.Folder)
	}
	if len(n.Tags) > 0 {
		meta += styleTag.Render(" #" + n.Tags[0])
	}
	metaW := lipgloss.Width(meta)
	titleW := width - 16 - metaW
	if titleW < 6 {
		titleW = 6
	}
	title = runewidth.Truncate(title, titleW, "…")
	titleVisualW := runewidth.StringWidth(title)
	if titleVisualW < titleW {
		title += strings.Repeat(" ", titleW-titleVisualW)
	}
	// pad date to 14 chars visually before styling
	padded := dateStr + strings.Repeat(" ", 14-len([]rune(dateStr)))
	_ = padded
	return fmt.Sprintf("%s  %s%s", dateStyled, title, meta)
}

func coloredDate(s string, t time.Time) string {
	now := time.Now()
	// pad to fixed 14-char visual width before coloring
	runes := []rune(s)
	padded := string(runes) + strings.Repeat(" ", 14-len(runes))
	switch {
	case sameDay(t, now):
		return styleDateToday.Render(padded)
	case t.After(now.AddDate(0, 0, -7)):
		return styleDateWeek.Render(padded)
	case t.After(now.AddDate(0, -1, 0)):
		return styleDateMonth.Render(padded)
	default:
		return styleDateOld.Render(padded)
	}
}

func firstBodyLine(body string) string {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		// skip markdown headings and empty lines
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "---") {
			continue
		}
		// strip list markers and quotes for preview
		line = strings.TrimPrefix(line, "- ")
		line = strings.TrimPrefix(line, "* ")
		line = strings.TrimPrefix(line, "> ")
		line = strings.TrimPrefix(line, "[ ] ")
		line = strings.TrimPrefix(line, "[x] ")
		line = strings.TrimPrefix(line, "[X] ")
		return line
	}
	return ""
}

func emptyHint() string {
	switch config.Source() {
	case config.SourceApple:
		return "No notes — press s to sync from Apple Notes"
	case config.SourceObsidian, config.SourceMarkdown:
		return "No notes — press p to set vault path, then s to sync"
	default:
		return "No notes — press p to configure a source, then s to sync"
	}
}

func dateGroup(t time.Time) string {
	now := time.Now()
	switch {
	case sameDay(t, now):
		return "Today"
	case sameDay(t, now.AddDate(0, 0, -1)):
		return "Yesterday"
	case t.After(now.AddDate(0, 0, -7)):
		return t.Format("Monday")
	case t.After(now.AddDate(0, -1, 0)):
		return "This month"
	case t.Year() == now.Year():
		return t.Format("January")
	default:
		return t.Format("January 2006")
	}
}

func renderGroupHeader(group string, width int) string {
	label := " " + group + " "
	dashes := width - len([]rune(label)) - 3
	if dashes < 2 {
		dashes = 2
	}
	return styleMuted.Render("──" + label + strings.Repeat("─", dashes))
}

// applySortOrder sorts m.notes and restores cursor by ID.
func (m Model) applySortOrder() Model {
	if m.sortByDate {
		return m // SQL already returns date-sorted
	}
	var curID string
	if m.cursor < len(m.notes) {
		curID = m.notes[m.cursor].ID
	}
	sort.Slice(m.notes, func(i, j int) bool {
		return strings.ToLower(m.notes[i].Title) < strings.ToLower(m.notes[j].Title)
	})
	for i, n := range m.notes {
		if n.ID == curID {
			m.cursor = i
			break
		}
	}
	return m
}

func copyToClipboardCmd(text string) tea.Cmd {
	return func() tea.Msg {
		cmd := exec.Command("pbcopy")
		cmd.Stdin = strings.NewReader(text)
		if err := cmd.Run(); err != nil {
			return errMsg{err}
		}
		return nil
	}
}

func runeLimit(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n-1]) + "…"
}

func smartDate(t time.Time) string {
	now := time.Now()
	switch {
	case sameDay(t, now):
		return t.Format("      15:04")
	case t.After(now.AddDate(0, 0, -6)):
		return t.Format("Mon   15:04")
	case t.Year() == now.Year():
		return t.Format("Jan 02 15:04")
	default:
		return t.Format("Jan 02  2006")
	}
}

func sameDay(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
