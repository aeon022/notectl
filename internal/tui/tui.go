package tui

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/aeon022/missionctl-core/humanize"
	"github.com/aeon022/missionctl-core/lastsync"
	"github.com/aeon022/missionctl-core/overlay"
	"github.com/aeon022/missionctl-core/theme"
	"github.com/aeon022/missionctl-core/uistate"
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
	"github.com/sahilm/fuzzy"
)

// ── Views ─────────────────────────────────────────────────────────────────────

type view int

const (
	viewList     view = iota
	viewDetail   view = iota
	viewNew      view = iota
	viewSettings view = iota
	viewHelp     view = iota
	viewTags     view = iota
	viewGraph    view = iota
)

// ── Styles ────────────────────────────────────────────────────────────────────

var (
	// Shared across the suite via missionctl-core/theme.
	colorBlue   = theme.Blue
	colorGreen  = theme.Green
	colorRed    = theme.Red
	colorMuted  = theme.Muted
	colorSubtle = theme.Subtle
	colorAmber  = theme.Amber
	colorTabBg  = lipgloss.AdaptiveColor{Light: "252", Dark: "235"}

	styleHeader   = lipgloss.NewStyle().Bold(true).Foreground(colorBlue)
	styleDivider  = lipgloss.NewStyle().Foreground(colorSubtle)
	styleHelp     = lipgloss.NewStyle().Foreground(colorMuted)
	styleErr      = lipgloss.NewStyle().Foreground(colorRed)
	styleOK       = lipgloss.NewStyle().Foreground(colorGreen)
	styleMuted    = lipgloss.NewStyle().Foreground(colorMuted)
	styleBold     = lipgloss.NewStyle().Bold(true)
	styleSelected = lipgloss.NewStyle().
			Background(theme.SelectedBg).
			Foreground(theme.SelectedFg).
			Bold(true)
	styleTag     = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "33", Dark: "75"})
	styleFolder  = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "136", Dark: "178"})
	styleLabel   = lipgloss.NewStyle().Foreground(colorBlue).Width(9)
	styleSyncing = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "214", Dark: "220"})

	styleTabActive = lipgloss.NewStyle().Bold(true).
			Foreground(theme.OnAccent).
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
type noteRestoredMsg struct {
	note *models.Note
	err  error
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
	notes        []models.Note // filtered (by searchQ) view of allNotes
	allNotes     []models.Note // everything loaded for the current folder scope
	cursor       int
	hoverRow     int // m.notes index under the mouse cursor, -1 when none
	lastClickRow int // m.notes index of the previous left-click, -1 when none — double-click opens the note detail, same window/pattern taskctl uses
	lastClickAt  time.Time
	searchQ      string
	searching    bool
	searchInput  textinput.Model
	folders      []string
	activeTab    int // 0 = All, 1+ = folder
	folderCounts map[string]int

	// pendingFolderRestore holds the persisted last-active folder name
	// (see uistate) until m.folders first loads, resolved to an index and
	// cleared then — same one-shot pattern as openPath below.
	pendingFolderRestore string

	// tag browser ("t")
	tagCursor int

	// link graph ("L" from detail) — a one-hop neighbor explorer: focus
	// note plus its outgoing/incoming wiki-links, cursor moves over the
	// combined neighbor list, enter re-focuses the graph on that neighbor.
	graphFocus    *models.Note
	graphOut      []models.Note
	graphIn       []models.Note
	graphCursor   int
	graphPrevView view // where esc/q returns to (list or detail)

	// openPath, when set (via `notectl --open <relpath>`, e.g. jumping in
	// from diaryctl's linked entry), opens that note's detail view as soon
	// as notes finish loading, then clears itself so it only fires once.
	openPath string

	// detail / preview
	detail           *models.Note
	detailLineCursor int           // current line in detail body (for j/k + checkbox toggle)
	detailYOffset    int           // current visual Y offset in detail view
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
	editorYOffset int           // mirrors bodyArea's internal viewport scroll (for mouse clicks)

	// settings
	vaultInput textinput.Model
	sourceIdx  int

	// list options
	sortByDate bool    // true = mod_time desc (default), false = title asc
	paneRatio  float64 // two-pane left width ratio (default 0.38)
	confirmID  string  // non-empty = waiting for delete confirmation

	// undo: "u" within undoWindow of a delete restores the deleted note —
	// same pattern and window taskctl uses for its own delete-undo.
	// statusTime doubles as its expiry clock (see the tea.KeyMsg case).
	lastDeleted *models.Note

	// status
	status     string
	statusTime time.Time
	err        error
	syncing    bool
	lastSynced time.Time // zero = never synced this install
	sp         spinner.Model
	loading    bool

	// "?" transient help popup
	helpVP   viewport.Model
	helpPopW int
	helpPopH int
}

func New(openPath string) Model {
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

	var state persistedState
	uistate.Load(config.UIStatePath(), &state)

	return Model{
		sp:                   sp,
		searchInput:          si,
		titleInput:           ti,
		tagsInput:            tags,
		bodyArea:             body,
		vaultInput:           vi,
		sourceIdx:            srcIdx,
		sortByDate:           true,
		paneRatio:            0.38,
		loading:              true,
		hoverRow:             -1,
		lastClickRow:         -1,
		openPath:             openPath,
		pendingFolderRestore: state.LastFolder,
	}
}

// persistedState is what New() restores from and saveUIState saves to — see
// missionctl-core/uistate.
type persistedState struct {
	LastFolder string `json:"last_folder"`
}

func (m Model) saveUIState() {
	_ = uistate.Save(config.UIStatePath(), persistedState{LastFolder: m.activeFolder()})
}

func Run(openPath string) error {
	m := New(openPath)
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseAllMotion())
	_, err := p.Run()
	return err
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(loadNotesCmd(""), doSyncCmd(), tea.WindowSize(), m.sp.Tick, loadLastSyncedCmd())
}

type lastSyncedLoadedMsg struct{ t time.Time }

func loadLastSyncedCmd() tea.Cmd {
	return func() tea.Msg {
		t, _ := lastsync.Load(config.LastSyncedPath())
		return lastSyncedLoadedMsg{t: t}
	}
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
		m.loading = false
		// Remember which note was selected so we can restore it after the list changes
		// (e.g. after a sync that reorders notes by mod_time).
		var prevID string
		if m.cursor < len(m.notes) {
			prevID = m.notes[m.cursor].ID
		}
		if m.view == viewDetail && m.detail != nil {
			prevID = m.detail.ID
		}
		m.allNotes = msg.notes
		m.notes = filterNotes(m.allNotes, m.searchQ)
		m.folders = msg.folders
		if msg.folderCounts != nil {
			m.folderCounts = msg.folderCounts
		}
		if m.pendingFolderRestore != "" {
			restore := m.pendingFolderRestore
			m.pendingFolderRestore = ""
			for i, f := range m.folders {
				if f == restore {
					m.activeTab = i + 1
					return m, loadNotesCmd(restore)
				}
			}
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
		if m.openPath != "" {
			// notesLoadedMsg fires once immediately (pre-sync, possibly
			// before the note exists in the DB yet) and again after
			// syncDoneMsg reloads — only clear openPath on an actual match
			// so the post-sync pass still gets a chance to find it.
			for _, n := range m.allNotes {
				if n.Path == m.openPath {
					var cmd tea.Cmd
					m, cmd = m.openNoteDetail(n)
					m.openPath = ""
					return m, cmd
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

	case lastSyncedLoadedMsg:
		m.lastSynced = msg.t

	case syncDoneMsg:
		m.syncing = false
		if msg.err != nil {
			m.err = msg.err
		} else {
			m.setStatus(fmt.Sprintf("Synced %d notes", msg.count))
			m.lastSynced = time.Now()
			_ = lastsync.Save(config.LastSyncedPath(), m.lastSynced)
			return m, loadNotesCmd(m.activeFolder())
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
			return m, loadNotesCmd(m.activeFolder())
		}

	case noteRestoredMsg:
		if msg.err != nil {
			m.err = msg.err
		} else {
			name := ""
			if msg.note != nil {
				name = msg.note.Title
			}
			m.setStatus("Restored: " + name)
			return m, loadNotesCmd(m.activeFolder())
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
			return m, loadNotesCmd(m.activeFolder())
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
			if m.view != viewList || msg.Action != tea.MouseActionPress {
				return m, nil
			}
			if i := m.tabHitTest(msg.X, msg.Y); i >= 0 {
				if i != m.activeTab {
					m.activeTab = i
					m.cursor = 0
					m.saveUIState()
					return m, loadNotesCmd(m.activeFolder())
				}
				return m, nil
			}
			if i := m.rowHitTest(msg.X, msg.Y); i >= 0 {
				now := time.Now()
				if i == m.lastClickRow && now.Sub(m.lastClickAt) < doubleClickWindow {
					m.cursor = i
					m.lastClickRow = -1 // consumed, so a third click starts fresh
					n := m.notes[i]
					m.detail = &n
					m.detailBlocks = nil
					m.detailLineCursor = 0
					m.detailYOffset = 0
					m.vp.GotoTop()
					m.view = viewDetail
					if config.Source() == config.SourceApple {
						m.vp.SetContent(styleMuted.Render("Loading…"))
						return m, loadAppleBodyCmd(n.ID)
					}
					content, _ := renderDetailBody(n.Body, 0, m.detailBodyWidth())
					m.vp.SetContent(content)
					return m, nil
				}
				m.cursor = i
				m.lastClickRow = i
				m.lastClickAt = now
				var cmd tea.Cmd
				m, cmd = m.refreshPreview()
				return m, cmd
			}
		case tea.MouseButtonNone:
			if msg.Action == tea.MouseActionMotion && m.view == viewList {
				m.hoverRow = m.rowHitTest(msg.X, msg.Y)
			}
		}

	case spinner.TickMsg:
		if m.syncing || m.loading {
			var cmd tea.Cmd
			m.sp, cmd = m.sp.Update(msg)
			return m, cmd
		}
		return m, nil

	case tea.KeyMsg:
		m.err = nil
		// The delete-undo toast gets the longer undoWindow instead of the
		// usual 3s — it's also the window "u" checks below, so the message
		// and the capability it describes expire together.
		clearAfter := 3 * time.Second
		if m.lastDeleted != nil {
			clearAfter = undoWindow
		}
		if time.Since(m.statusTime) > clearAfter {
			m.status = ""
			m.lastDeleted = nil
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
		case viewHelp:
			switch msg.String() {
			case "ctrl+c":
				return m, tea.Quit
			case "q", "esc", "?":
				m.view = viewList
				return m, nil
			}
			var cmd tea.Cmd
			m.helpVP, cmd = m.helpVP.Update(msg)
			return m, cmd
		case viewTags:
			return m.updateTags(msg)
		case viewGraph:
			return m.updateGraph(msg)
		}
	}

	if m.view == viewDetail {
		var cmd tea.Cmd
		m.vp, cmd = m.vp.Update(msg)
		return m, cmd
	}
	return m, nil
}

// openNoteDetail switches to the detail view for n, matching the "enter"
// key's behavior — used by the --open startup flow (jumping in from a
// linked entry in another tool) which has no keypress to hook.
func (m Model) openNoteDetail(n models.Note) (Model, tea.Cmd) {
	m.detail = &n
	m.detailBlocks = nil
	m.detailLineCursor = 0
	m.detailYOffset = 0
	m.vp.GotoTop()
	m.view = viewDetail
	if config.Source() == config.SourceApple {
		m.vp.SetContent(styleMuted.Render("Loading…"))
		return m, loadAppleBodyCmd(n.ID)
	}
	content, _ := renderDetailBody(n.Body, 0, m.detailBodyWidth())
	m.vp.SetContent(content)
	return m, nil
}

func (m Model) updateList(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.searching {
		switch msg.String() {
		case "enter":
			// Filtering already happened live as the user typed (below) —
			// enter just closes the input box, no DB round-trip needed.
			m.searching = false
			m.cursor = 0
		case "esc":
			m.searching = false
			m.searchInput.SetValue("")
			m.searchQ = ""
			m.cursor = 0
			m.notes = filterNotes(m.allNotes, "")
			m = m.applySortOrder()
		default:
			var cmd tea.Cmd
			m.searchInput, cmd = m.searchInput.Update(msg)
			m.searchQ = m.searchInput.Value()
			m.cursor = 0
			m.notes = filterNotes(m.allNotes, m.searchQ)
			m = m.applySortOrder()
			return m, cmd
		}
		return m, nil
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
		m.saveUIState()
		return m, loadNotesCmd(m.activeFolder())
	case "shift+tab":
		tabs := len(m.folders) + 1
		m.activeTab = (m.activeTab - 1 + tabs) % tabs
		m.cursor = 0
		m.saveUIState()
		return m, loadNotesCmd(m.activeFolder())
	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		// jump to the nth visible (on-screen) note, date-group headers not
		// counted — mirrors rowHitTest's own scroll-window math so a digit
		// lands on the same note a click at that position would.
		n := int(msg.String()[0] - '0')
		w := m.width
		withPreview := true
		if m.isTwoPane() {
			const pad = 1
			w = max(1, m.leftWidth()-pad*2)
			withPreview = false
		}
		_, cursorLine, lineToNote := m.buildListLinesWithMapping(w, withPreview)
		listH := m.listHeight()
		start := 0
		if cursorLine >= listH {
			start = cursorLine - listH + 1
		}
		count := 0
		for _, noteIdx := range lineToNote[start:] {
			if noteIdx < 0 {
				continue
			}
			count++
			if count == n {
				m.cursor = noteIdx
				break
			}
		}
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
			m.lastDeleted = &n
			m.setStatus("Deleted: " + n.Title + " — press u to undo")
			ref := n.Path
			if config.Source() == config.SourceApple {
				ref = n.ID
			}
			return m, deleteNoteCmd(n.ID, ref)
		}
	case "u":
		if m.lastDeleted != nil {
			n := m.lastDeleted
			m.lastDeleted = nil
			m.status = ""
			return m, undoDeleteNoteCmd(*n)
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
	case "t":
		m.tagCursor = 0
		m.view = viewTags
		return m, nil
	case "L":
		if len(m.notes) > 0 {
			m = m.setGraphFocus(m.notes[m.cursor])
			m.graphPrevView = viewList
			m.view = viewGraph
			return m, nil
		}
	case "?":
		m = m.openHelp()
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
			m.notes = filterNotes(m.allNotes, "")
			m = m.applySortOrder()
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

	case "L":
		if m.detail != nil {
			m = m.setGraphFocus(*m.detail)
			m.graphPrevView = viewDetail
			m.view = viewGraph
			return m, nil
		}

	case "d":
		if m.detail != nil {
			// Detail view had no confirm step at all before — a stray "d"
			// while reading a note deleted it immediately, unlike the list
			// view's same key which requires a second press. Now matches.
			if m.confirmID != m.detail.ID {
				m.confirmID = m.detail.ID
				m.setStatus(fmt.Sprintf("Delete \"%s\"?  d:confirm  esc:cancel", runeLimit(m.detail.Title, 30)))
				return m, nil
			}
			m.confirmID = ""
			ref := m.detail.Path
			if config.Source() == config.SourceApple {
				ref = m.detail.ID
			}
			n := *m.detail
			id, path, title := n.ID, ref, n.Title
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
			m.lastDeleted = &n
			m.setStatus("Deleted: " + title + " — press u to undo")
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

// tagCount is one entry in the tag browser: a tag name and how many notes
// carry it.
type tagCount struct {
	name  string
	count int
}

// allTags tallies every tag across notes, sorted by count descending then
// name ascending.
func allTags(notes []models.Note) []tagCount {
	counts := map[string]int{}
	for _, n := range notes {
		for _, t := range n.Tags {
			counts[t]++
		}
	}
	out := make([]tagCount, 0, len(counts))
	for name, c := range counts {
		out = append(out, tagCount{name: name, count: c})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].count != out[j].count {
			return out[i].count > out[j].count
		}
		return out[i].name < out[j].name
	})
	return out
}

func (m Model) updateTags(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	tags := allTags(m.allNotes)
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "q", "esc":
		m.view = viewList
		return m, nil
	case "j", "down":
		if m.tagCursor < len(tags)-1 {
			m.tagCursor++
		}
	case "k", "up":
		if m.tagCursor > 0 {
			m.tagCursor--
		}
	case "enter":
		if m.tagCursor < len(tags) {
			m.searchQ = tags[m.tagCursor].name
			m.searchInput.SetValue(m.searchQ)
			m.notes = filterNotes(m.allNotes, m.searchQ)
			m.cursor = 0
		}
		m.view = viewList
		return m, nil
	}
	return m, nil
}

// updateGraph drives the link-graph explorer: j/k moves the cursor over the
// focus note's combined outgoing+incoming neighbors, enter re-focuses the
// graph on the selected neighbor (one-hop traversal, chainable), "d" opens
// that neighbor's full detail view, esc/q returns to wherever "L" was
// pressed from.
func (m Model) updateGraph(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	neighbors := m.graphNeighbors()
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "q", "esc":
		m.view = m.graphPrevView
		return m, nil
	case "j", "down":
		if m.graphCursor < len(neighbors)-1 {
			m.graphCursor++
		}
	case "k", "up":
		if m.graphCursor > 0 {
			m.graphCursor--
		}
	case "enter":
		if m.graphCursor < len(neighbors) {
			m = m.setGraphFocus(neighbors[m.graphCursor])
		}
	case "d":
		if m.graphCursor < len(neighbors) {
			return m.openNoteDetail(neighbors[m.graphCursor])
		}
	}
	return m, nil
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
			formatted = leading + styleStrike.Render("☑ "+renderInline(text))
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
			formatted = leading + styleMuted.Render("☐ "+renderInline(text))
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
	case viewHelp:
		// "?" is only reachable from the main list, so the list is always
		// the correct background to keep visible behind the popup. No
		// enclosing border on the list view, so inset 0 is safe.
		return overlay.Center(m.renderList(), m.renderHelpPopup(), m.width, m.height, 0)
	case viewTags:
		return overlay.Center(m.renderList(), m.renderTags(), m.width, m.height, 0)
	case viewGraph:
		return m.renderGraph()
	default:
		return m.renderList()
	}
}

func (m Model) renderTags() string {
	tags := allTags(m.allNotes)
	var b strings.Builder
	b.WriteString(styleHeader.Render("Tags") + "\n\n")
	if len(tags) == 0 {
		b.WriteString(styleHelp.Render("No tagged notes yet.") + "\n")
	}
	for i, t := range tags {
		row := fmt.Sprintf("#%s  (%d)", t.name, t.count)
		if i == m.tagCursor {
			b.WriteString(styleSelected.Render("› "+row) + "\n")
		} else {
			b.WriteString("  " + row + "\n")
		}
	}
	b.WriteString("\n" + styleHelp.Render("j/k move  enter filter by tag  esc/q close"))
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorBlue).
		Padding(1, 2).
		Width(min(50, m.width-4)).
		Render(b.String())
}

// renderGraph draws the link-graph explorer: the focus note centered, its
// outgoing wiki-links above and incoming backlinks below, connected with
// simple box-drawing lines. Not a force-directed layout — a terminal has no
// room for that — just a one-hop neighbor view you can walk through.
func (m Model) renderGraph() string {
	if m.graphFocus == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n  " + styleHeader.Render("Link Graph") + "\n\n")

	cursor := 0
	renderNeighbor := func(n models.Note, arrow string) {
		row := arrow + " " + n.Title
		if cursor == m.graphCursor {
			b.WriteString("  " + styleSelected.Render("› "+row) + "\n")
		} else {
			b.WriteString("    " + styleMuted.Render(row) + "\n")
		}
		cursor++
	}

	if len(m.graphOut) == 0 {
		b.WriteString("    " + styleHelp.Render("(no outgoing links)") + "\n")
	}
	for _, n := range m.graphOut {
		renderNeighbor(n, "──▶")
	}

	b.WriteString("\n  " + styleTag.Render("┃ "+m.graphFocus.Title) + "\n\n")

	if len(m.graphIn) == 0 {
		b.WriteString("    " + styleHelp.Render("(no backlinks)") + "\n")
	}
	for _, n := range m.graphIn {
		renderNeighbor(n, "◀──")
	}

	b.WriteString("\n" + styleHelp.Render("j/k move  enter re-focus graph here  d open note  esc/q back"))
	return b.String()
}

func (m Model) helpContent() string {
	key := func(k string) string { return styleBold.Render(fmt.Sprintf("%-11s", k)) }
	row := func(k, desc string) string { return "  " + key(k) + styleHelp.Render(desc) + "\n" }
	section := func(t string) string { return "\n  " + styleHeader.Render(t) + "\n" }

	var b strings.Builder
	b.WriteString(section("Navigation"))
	b.WriteString(row("j / k", "move down / up"))
	b.WriteString(row("g / G", "jump to top / bottom"))
	b.WriteString(row("pgdn/pgup", "page down / up"))
	b.WriteString(row("tab", "next folder"))
	b.WriteString(row("shift+tab", "previous folder"))
	b.WriteString(row("< / >", "resize panes (two-pane layout)"))
	b.WriteString(section("Notes"))
	b.WriteString(row("enter", "open note"))
	b.WriteString(row("n", "new note"))
	b.WriteString(row("e", "edit note"))
	b.WriteString(row("d", "delete note (asks to confirm)"))
	b.WriteString(row("o", "open in external app"))
	b.WriteString(row("y", "copy title to clipboard"))
	b.WriteString(section("Other"))
	b.WriteString(row("S", "toggle sort (date / title A–Z)"))
	b.WriteString(row("p", "settings (vault path, source)"))
	b.WriteString(row("s", "sync"))
	b.WriteString(row("/", "search (esc clears)"))
	b.WriteString(row("t", "browse tags"))
	b.WriteString(row("L", "link graph (browse [[wiki-links]])"))
	b.WriteString(row("?", "toggle this help"))
	b.WriteString(row("q", "quit"))
	return b.String()
}

// openHelp sizes and populates the transient help popup (see
// renderHelpPopup/overlay.Center) from the ACTUAL rendered background
// height, not the terminal size.
func (m Model) openHelp() Model {
	bgLines := strings.Split(m.renderList(), "\n")

	safeH := max(6, len(bgLines))
	popH := min(safeH, 24)
	popW := min(70, m.width)
	if popW < 40 {
		popW = 40
	}

	vp := viewport.New(popW-6, popH-5) // border 1+1, padding(1,2) → 2 rows/4 cols; -1 row for footer
	vp.SetContent(m.helpContent())

	m.helpVP = vp
	m.helpPopW = popW
	m.helpPopH = popH
	m.view = viewHelp
	return m
}

// renderHelpPopup renders the help viewport in a bordered box, meant to be
// composited over the list view via overlay.Center rather than replacing
// the whole screen — the list stays visible around it.
func (m Model) renderHelpPopup() string {
	footer := "esc / ?  close"
	if m.helpVP.TotalLineCount() > m.helpVP.Height {
		footer = fmt.Sprintf("j/k scroll (%d%%)  ·  %s", int(m.helpVP.ScrollPercent()*100), footer)
	}
	body := m.helpVP.View() + "\n" + styleHelp.Render(footer)
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorBlue).
		Padding(1, 2).
		Width(m.helpPopW).
		Render(body)
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

	if m.searching {
		b.WriteString("  " + m.searchInput.View() + "\n\n")
	}
	if m.searchQ != "" {
		b.WriteString(styleMuted.Render("  /"+m.searchQ) + "\n")
	}

	listH := m.listHeight()

	if m.loading {
		b.WriteString("\n  " + m.sp.View() + styleHelp.Render(" Loading notes…") + "\n")
	} else if len(m.notes) == 0 {
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
	paneH := m.listHeight()

	var b strings.Builder
	b.WriteString(" " + m.renderAppHeader(m.width-1) + "\n")
	b.WriteString(" " + m.renderTabBar(m.width-1) + "\n")
	b.WriteString(styleDivider.Render(strings.Repeat("─", m.width)) + "\n")

	// search row replaces one line of the pane
	if m.searching {
		b.WriteString("  " + m.searchInput.View() + "\n")
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
	if m.loading {
		leftLines = []string{" " + m.sp.View() + styleHelp.Render(" Loading notes…")}
	} else if len(m.notes) == 0 {
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

	b.WriteString("\n" + m.renderHelpBar(m.width))
	return b.String()
}

// buildListLines pre-renders list rows with optional date group headers and preview lines.
func (m Model) buildListLines(w int, withPreview bool) ([]string, int) {
	lines, cursorLine, _ := m.buildListLinesWithMapping(w, withPreview)
	return lines, cursorLine
}

// buildListLinesWithMapping is buildListLines plus a parallel lineToNote
// slice (note index for a main-row or preview line, -1 for a group-header
// or blank-separator line), so rowHitTest can map a clicked screen line
// back to a note without re-deriving this layout itself.
func (m Model) buildListLinesWithMapping(w int, withPreview bool) ([]string, int, []int) {
	var lines []string
	var lineToNote []int
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
					lineToNote = append(lineToNote, -1)
				}
				lines = append(lines, renderGroupHeader(g, w))
				lineToNote = append(lineToNote, -1)
				lastGroup = g
			}
		}

		if i == m.cursor {
			cursorLine = len(lines)
		}
		rowStyle := lipgloss.NewStyle()
		switch {
		case i == m.cursor:
			rowStyle = styleSelected
		case i == m.hoverRow:
			rowStyle = theme.Hover
		}
		lines = append(lines, formatNoteRow(n, w, rowStyle, m.searchQ))
		lineToNote = append(lineToNote, i)

		if withPreview && n.Body != "" {
			preview := firstBodyLine(n.Body)
			if preview != "" {
				avail := w - 16
				if avail > 10 {
					preview = runewidth.Truncate(preview, avail, "…")
				}
				pLine := strings.Repeat(" ", 16) + preview
				switch {
				case i == m.cursor:
					pLine = styleSelected.Width(w).Render(pLine)
				case i == m.hoverRow:
					pLine = theme.Hover.Width(w).Render(pLine)
				default:
					pLine = styleMuted.Render(pLine)
				}
				lines = append(lines, pLine)
				lineToNote = append(lineToNote, i)
			}
		}
	}
	return lines, cursorLine, lineToNote
}

// listStartY returns the number of preamble lines above the note list —
// header, tab bar, divider, and (mode-dependent) an optional search
// input/filter chip — shared by the render paths and rowHitTest so they
// can't drift apart. Two-pane's search row costs 1 line (no trailing
// blank, no separate filter chip line); single-pane's costs 2 plus an
// optional filter chip line.
func (m Model) listStartY() int {
	y := 3 // header + tab bar + divider
	if m.isTwoPane() {
		if m.searching {
			y++
		}
		return y
	}
	if m.searching {
		y += 2
	}
	if m.searchQ != "" {
		y++
	}
	return y
}

// listHeight returns the available line budget for the note list itself,
// matching listH (single-pane) / paneH (two-pane) in the render paths.
func (m Model) listHeight() int {
	if m.isTwoPane() {
		h := m.height - 3 - helpBarHeight - 1 // -1: blank padding line above the help bar
		if m.searching {
			h--
		}
		if h < 1 {
			h = 1
		}
		return h
	}
	h := m.height - m.listStartY() - helpBarHeight - 1 // -1: blank padding line above the help bar
	if h < 1 {
		h = 1
	}
	return h
}

// tabHitTest returns the folder-tab index at column x on the tab bar row
// (row 1: header is row 0), or -1 if the click didn't land on a tab.
func (m Model) tabHitTest(x, y int) int {
	if y != 1 {
		return -1
	}
	tabs := append([]string{"All"}, m.folders...)
	col := 1 // renderTabBar's line is written with a leading " "
	for i, t := range tabs {
		label := t
		folderKey := t
		if i == 0 {
			folderKey = ""
		}
		if c := m.folderCounts[folderKey]; c > 0 {
			label = fmt.Sprintf("%s %d", t, c)
		}
		w := lipgloss.Width(styleTabInact.Render(label))
		if i == m.activeTab {
			w = lipgloss.Width(styleTabActive.Render(label))
		}
		if x >= col && x < col+w {
			return i
		}
		col += w + 2 // "  " join separator
	}
	return -1
}

// rowHitTest returns the m.notes index at screen position (x, y), or -1
// if the click missed. Mirrors buildListLinesWithMapping's line layout,
// listStartY/listHeight's preamble+budget accounting, and each render
// mode's scroll window (start := cursorLine - listHeight + 1) so a click
// lands on the note it visually appears to be over. In two-pane mode, x
// must land inside the left (list) pane, not the preview pane.
func (m Model) rowHitTest(x, y int) int {
	if m.isTwoPane() && x >= m.leftWidth() {
		return -1
	}
	idx := y - m.listStartY()
	if idx < 0 || len(m.notes) == 0 {
		return -1
	}
	w := m.width
	withPreview := true
	if m.isTwoPane() {
		const pad = 1
		w = max(1, m.leftWidth()-pad*2)
		withPreview = false
	}
	_, cursorLine, lineToNote := m.buildListLinesWithMapping(w, withPreview)
	listH := m.listHeight()
	start := 0
	if cursorLine >= listH {
		start = cursorLine - listH + 1
	}
	lineIdx := start + idx
	if lineIdx >= len(lineToNote) {
		return -1
	}
	return lineToNote[lineIdx]
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
	} else if !m.lastSynced.IsZero() {
		bar += "  " + styleMuted.Render("synced "+humanize.TimeAgo(m.lastSynced))
	}
	_ = w
	return bar
}

// renderHelpBar renders the bottom help area. The key list is split across
// two lines — it was one long line that overflowed on typical terminal
// widths, unlike the other suite tools' shorter footers. An error/status
// message still renders as a single line; helpBarHeight/listHeight below
// always reserve room for 2 lines regardless, so the list above doesn't
// shift depending on what's currently showing.
func (m Model) renderHelpBar(w int) string {
	right := ""
	if len(m.notes) > 0 {
		sortIcon := "↓date"
		if !m.sortByDate {
			sortIcon = "↓A-Z"
		}
		right = styleHelp.Render(fmt.Sprintf("%d/%d  %s", m.cursor+1, len(m.notes), sortIcon))
	}
	if m.err != nil {
		return styleErr.Render("✗ " + m.err.Error())
	}
	if m.status != "" {
		if m.confirmID != "" {
			return styleSyncing.Render("⚠ " + m.status)
		}
		return styleOK.Render("✓ " + m.status)
	}
	line1 := styleHelp.Render("enter:open  n:new  e:edit  d:delete  u:undo  y:copy  S:sort")
	line2 := styleHelp.Render("o:editor  s:sync  p:settings  /:search  tab:folder  ?:help  q:quit")
	pad := w - lipgloss.Width(line2) - lipgloss.Width(right)
	if pad < 0 {
		pad = 0
	}
	return line1 + "\n" + line2 + strings.Repeat(" ", pad) + right
}

// helpBarHeight is the line budget reserved below the list for
// renderHelpBar's output, shared with listHeight so the two can't drift.
const helpBarHeight = 2

// doubleClickWindow opens the note detail on a second click within this
// window, same pattern and duration taskctl uses for its own double-click.
const doubleClickWindow = 400 * time.Millisecond

// undoWindow is how long after a delete "u" still restores it — same
// duration taskctl uses for its own delete-undo.
const undoWindow = 5 * time.Second

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
	b.WriteString(detailLeftPad + styleMuted.Render(m.detail.ModTime.Format("Mon, 02 Jan 2006 15:04")) + "\n")
	if backlinks := backlinksFor(*m.detail, m.allNotes); len(backlinks) > 0 {
		names := make([]string, len(backlinks))
		for i, n := range backlinks {
			names[i] = n.Title
		}
		b.WriteString(detailLeftPad + styleMuted.Render("Linked from: ") + styleTag.Render(strings.Join(names, ", ")) + "\n")
	}
	b.WriteString("\n")
	b.WriteString(styleDivider.Render(strings.Repeat("─", m.width)) + "\n")
	m.vp.Width = m.detailBodyWidth()
	m.vp.Height = m.bodyHeight()
	b.WriteString(renderScrollbar(m.vp, detailLeftPad))
	pct := ""
	if m.vp.TotalLineCount() > m.vp.Height {
		pct = fmt.Sprintf(" %d%%", int(m.vp.ScrollPercent()*100))
	}
	helpStr := "esc:back  e:edit  d:delete  o:notes  L:link graph  j/k:scroll  space:toggle checkbox  q:quit"
	b.WriteString("\n\n" + detailLeftPad + styleHelp.Render(helpStr) + styleMuted.Render(pct))
	return b.String()
}

// backlinksFor returns every note in all whose body references target via
// an Obsidian-style [[Title]] wiki-link, excluding target itself.
func backlinksFor(target models.Note, all []models.Note) []models.Note {
	needle := "[[" + target.Title + "]]"
	var out []models.Note
	for _, n := range all {
		if n.ID == target.ID {
			continue
		}
		if strings.Contains(n.Body, needle) {
			out = append(out, n)
		}
	}
	return out
}

var wikiLinkRe = regexp.MustCompile(`\[\[([^\]]+)\]\]`)

// outgoingLinksFor returns every note in all that source's body links to via
// an Obsidian-style [[Title]] wiki-link. Titles with no matching note (a
// link to a not-yet-created note) are silently skipped.
func outgoingLinksFor(source models.Note, all []models.Note) []models.Note {
	var out []models.Note
	for _, m := range wikiLinkRe.FindAllStringSubmatch(source.Body, -1) {
		title := m[1]
		for _, n := range all {
			if n.ID != source.ID && n.Title == title {
				out = append(out, n)
				break
			}
		}
	}
	return out
}

// setGraphFocus re-centers the link graph on n, computing its one-hop
// neighbors fresh (m.allNotes is the unfiltered set, so the graph isn't
// limited by the current folder tab or search query).
func (m Model) setGraphFocus(n models.Note) Model {
	m.graphFocus = &n
	m.graphOut = outgoingLinksFor(n, m.allNotes)
	m.graphIn = backlinksFor(n, m.allNotes)
	m.graphCursor = 0
	return m
}

// graphNeighbors is the combined, cursor-addressable neighbor list: outgoing
// links first, then incoming (matches render order).
func (m Model) graphNeighbors() []models.Note {
	out := make([]models.Note, 0, len(m.graphOut)+len(m.graphIn))
	out = append(out, m.graphOut...)
	out = append(out, m.graphIn...)
	return out
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
		switch {
		case i == 0:
			// The first visible row is a note's title line, which renders
			// with a full-width selected/highlighted background when the
			// cursor starts there — a bare track glyph butted up against
			// that fill looked like a break in the bar rather than part of
			// it. Leaving it blank lets the bar visually start clean on
			// the row below instead.
			glyphs.WriteByte(' ')
		case i >= thumbTop && i < thumbTop+thumbH:
			glyphs.WriteString(thumb)
		default:
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
		return leading + styleMDQuote.Render("│ "+renderInline(strings.TrimPrefix(t, "> ")))
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
		return leading + styleStrike.Render("☑ "+renderInline(t[6:]))
	case config.Source() == config.SourceApple && (strings.HasPrefix(t, "• ") || strings.HasPrefix(t, "- ") || strings.HasPrefix(t, "* ")):
		text := t
		if strings.HasPrefix(t, "• ") {
			text = strings.TrimPrefix(t, "• ")
		} else {
			text = t[2:]
		}
		if isItem, done := checklistLookup(text); isItem {
			if done {
				return leading + styleStrike.Render("☑ "+renderInline(text))
			}
			return leading + styleMuted.Render("☐ ") + renderInline(text)
		}
		return leading + "  • " + renderInline(text)
	case strings.HasPrefix(t, "- ") || strings.HasPrefix(t, "* "):
		return leading + "  • " + renderInline(t[2:])
	case strings.HasPrefix(t, "• "):
		return leading + "  " + renderInline(t)
	case strings.HasPrefix(t, "☑ "):
		return leading + styleStrike.Render("☑ "+renderInline(strings.TrimPrefix(t, "☑ ")))
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

// loadNotesCmd fetches notes for folder, unfiltered by search text —
// search is applied client-side (filterNotes) over the result, live as the
// user types, rather than round-tripping to SQLite on EVERY keystroke (the
// previous behavior). Store.Filter.Query / the SQL LIKE path still exists
// and is still used by `notectl search` and the MCP search tool, just not
// from here anymore.
func loadNotesCmd(folder string) tea.Cmd {
	return func() tea.Msg {
		s, err := store.New(config.DBPath())
		if err != nil {
			return errMsg{err}
		}
		defer s.Close()
		ctx := context.Background()
		ns, err := s.List(ctx, store.Filter{Folder: folder, Limit: 500})
		if err != nil {
			return errMsg{err}
		}
		folders, _ := s.ListFolders(ctx)
		counts, _ := s.CountByFolder(ctx)
		return notesLoadedMsg{notes: ns, folders: folders, folderCounts: counts}
	}
}

// filterNotes fuzzy-matches q against each note's title (github.com/
// sahilm/fuzzy), falling back to a plain substring match on body/tags for
// notes the title fuzzy-match missed. Body is free-form long text — fuzzy-
// matching it as one subsequence, like the title, would be nearly
// meaningless (almost any short query finds SOME subsequence across a full
// note, over-matching everything), so it stays substring-only, same
// reasoning as diaryctl's journal-entry search. Does not re-rank by match
// quality — notes are naturally ordered (date or title, per sortByDate),
// and re-sorting would scramble that.
func filterNotes(notes []models.Note, q string) []models.Note {
	q = strings.TrimSpace(q)
	if q == "" {
		return notes
	}
	titles := make([]string, len(notes))
	for i, n := range notes {
		titles[i] = n.Title
	}
	matched := make(map[int]bool, len(notes))
	for _, mt := range fuzzy.Find(q, titles) {
		matched[mt.Index] = true
	}
	ql := strings.ToLower(q)
	for i, n := range notes {
		if matched[i] {
			continue
		}
		if strings.Contains(strings.ToLower(n.Body), ql) || tagsContainSubstring(n.Tags, ql) {
			matched[i] = true
		}
	}
	out := make([]models.Note, 0, len(matched))
	for i, n := range notes {
		if matched[i] {
			out = append(out, n)
		}
	}
	return out
}

func tagsContainSubstring(tags []string, ql string) bool {
	for _, t := range tags {
		if strings.Contains(strings.ToLower(t), ql) {
			return true
		}
	}
	return false
}

// fuzzyMatchIndexes returns the rune indexes within s that q fuzzy-matched,
// or nil if q is empty or doesn't match at all.
func fuzzyMatchIndexes(q, s string) []int {
	if q == "" {
		return nil
	}
	matches := fuzzy.Find(q, []string{s})
	if len(matches) == 0 {
		return nil
	}
	return matches[0].MatchedIndexes
}

// highlightMatches renders s with the rune positions in idxs (from
// fuzzyMatchIndexes) styled via a warm, underlined variant of base, and
// every other character via base itself — fzf-style match highlighting.
//
// Renders one character at a time rather than nesting a highlighted span
// inside a single outer Render() call: lipgloss's Render() ends every
// string with a full SGR reset, so an inner Render() call's reset would
// wipe out the outer style for everything after the first highlighted
// character. Per-character rendering keeps every segment self-contained.
//
// idxs are indexes into s BEFORE any truncation — callers must resolve
// indexes against the same, untruncated string used to compute them.
func highlightMatches(s string, idxs []int, base lipgloss.Style) string {
	if len(idxs) == 0 {
		return base.Render(s)
	}
	hi := base.Foreground(colorAmber).Underline(true)
	matchSet := make(map[int]bool, len(idxs))
	for _, i := range idxs {
		matchSet[i] = true
	}
	var b strings.Builder
	for i, r := range []rune(s) {
		if matchSet[i] {
			b.WriteString(hi.Render(string(r)))
		} else {
			b.WriteString(base.Render(string(r)))
		}
	}
	return b.String()
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

// undoDeleteNoteCmd re-creates a deleted note — used by "u" within
// undoWindow of a delete. Recreates in the real source (vault file or
// Apple Notes) plus the local cache; the restored note gets a fresh ID
// since both backends assign their own identifier on create (same
// tradeoff taskctl/calctl already accept for their own delete-undo).
// Apple Notes bodies are cached as plain text (converted on read via
// BlocksToPlain); TextToHTML is the same conversion ReconcileBlocks
// already uses elsewhere, and WriteApple now prefixes the title onto the
// body itself on create, so this is a clean round-trip.
func undoDeleteNoteCmd(n models.Note) tea.Cmd {
	return func() tea.Msg {
		var restored *models.Note
		var err error
		if config.Source() == config.SourceApple {
			id, werr := notes.WriteApple("", n.Title, notes.TextToHTML(n.Body), n.Folder)
			if werr != nil {
				return noteRestoredMsg{err: werr}
			}
			restored = &models.Note{
				ID: id, Title: n.Title, Body: n.Body,
				Folder: n.Folder, Source: "apple",
				ModTime: time.Now(), Created: time.Now(),
			}
		} else {
			restored, err = notes.Write(config.VaultPath(), n.Title, n.Body, n.Tags, n.Folder)
			if err != nil {
				return noteRestoredMsg{err: err}
			}
		}
		s, err := store.New(config.DBPath())
		if err != nil {
			return noteRestoredMsg{err: err}
		}
		defer s.Close()
		_ = s.Upsert(context.Background(), restored)
		return noteRestoredMsg{note: restored}
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
			plainBody := body
			if id != "" {
				// Update path: WriteApple only touches body, and Apple
				// Notes derives the title from body's first line — the
				// caller has to keep that line current itself. (Create
				// does this internally now; see WriteApple's doc comment.)
				plainBody = title
				if body != "" {
					plainBody = title + "\n\n" + body
				}
			}
			htmlBody := notes.ReconcileBlocks(editBlocks, plainBody)
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

// formatNoteRow builds a note list row. rowStyle carries the selected-row
// treatment (background+foreground+bold) and is applied directly to the
// title segment — NOT via an outer Render() wrapping the whole composed
// row. That used to be how this worked (the caller wrapped the return
// value in styleSelected.Width(w).Render(...)), and it was broken the same
// way as an equivalent bug found and fixed in mailctl: dateStyled/meta
// below carry their OWN independent colors, and lipgloss's Render() ends
// every string with a full SGR reset — the first inner segment's reset
// clobbered the outer wrap's style for everything after it, so a selected
// row's highlight background didn't extend past the date column. Fixed by
// applying rowStyle per-segment instead, which also makes it safe to
// highlight fuzzy matches here even on the selected row.
func formatNoteRow(n *models.Note, width int, rowStyle lipgloss.Style, query string) string {
	dateStr := smartDate(n.ModTime)
	dateStyled := coloredDate(dateStr, n.ModTime) // independent color, unaffected by rowStyle

	title := n.Title
	if idx := strings.Index(title, "\n"); idx >= 0 {
		title = title[:idx]
	}
	title = strings.TrimSpace(title)

	meta := "" // independent colors (folder/tag), unaffected by rowStyle
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

	matchIdx := fuzzyMatchIndexes(query, title)
	titleTrunc := runewidth.Truncate(title, titleW, "…")
	titleStyled := highlightMatches(titleTrunc, matchIdx, rowStyle)
	if pad := titleW - runewidth.StringWidth(titleTrunc); pad > 0 {
		titleStyled += rowStyle.Render(strings.Repeat(" ", pad))
	}

	row := dateStyled + rowStyle.Render("  ") + titleStyled + meta

	// Pad to full width with rowStyle so a selected row's background spans
	// the whole line, not just up to the last character of content.
	if pad := width - lipgloss.Width(row); pad > 0 {
		row += rowStyle.Render(strings.Repeat(" ", pad))
	}
	return row
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
