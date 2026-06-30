package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/aeon022/notectl/internal/config"
	"github.com/aeon022/notectl/internal/models"
	"github.com/aeon022/notectl/internal/notes"
	"github.com/aeon022/notectl/internal/store"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ── Views ─────────────────────────────────────────────────────────────────────

type view int

const (
	viewList   view = iota
	viewDetail view = iota
	viewNew    view = iota
)

// ── Styles ────────────────────────────────────────────────────────────────────

var (
	colorBlue  = lipgloss.AdaptiveColor{Light: "21", Dark: "39"}
	colorMuted = lipgloss.AdaptiveColor{Light: "244", Dark: "240"}
	colorGreen = lipgloss.AdaptiveColor{Light: "28", Dark: "42"}
	colorRed   = lipgloss.AdaptiveColor{Light: "160", Dark: "203"}
	colorSubtle = lipgloss.AdaptiveColor{Light: "250", Dark: "237"}

	styleHeader  = lipgloss.NewStyle().Bold(true).Foreground(colorBlue)
	styleDivider = lipgloss.NewStyle().Foreground(colorSubtle)
	styleHelp    = lipgloss.NewStyle().Foreground(colorMuted)
	styleErr     = lipgloss.NewStyle().Foreground(colorRed)
	styleOK      = lipgloss.NewStyle().Foreground(colorGreen)
	styleMuted   = lipgloss.NewStyle().Foreground(colorMuted)
	styleBold    = lipgloss.NewStyle().Bold(true)
	styleSelected = lipgloss.NewStyle().Background(lipgloss.AdaptiveColor{Light: "254", Dark: "236"}).Bold(true)
	styleTag     = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "33", Dark: "75"})
	styleFolder  = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "136", Dark: "178"})
	styleLabel   = lipgloss.NewStyle().Foreground(colorBlue).Width(9)
	styleSyncing = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "214", Dark: "220"})

	styleTabActive = lipgloss.NewStyle().Bold(true).
			Foreground(lipgloss.Color("15")).
			Background(colorBlue).
			Padding(0, 2)
	styleTabInact = lipgloss.NewStyle().Foreground(colorMuted).Padding(0, 2)
)

// ── Messages ──────────────────────────────────────────────────────────────────

type notesLoadedMsg struct {
	notes   []models.Note
	folders []string
}
type syncDoneMsg struct {
	count int
	err   error
}
type writeDoneMsg struct {
	note *models.Note
	err  error
}
type errMsg struct{ err error }

// ── Model ─────────────────────────────────────────────────────────────────────

type Model struct {
	view   view
	width  int
	height int

	// list
	notes      []models.Note
	cursor     int
	searchQ    string
	searching  bool
	searchInput textinput.Model
	folders    []string
	activeTab  int // 0 = All, 1+ = folder

	// detail
	detail *models.Note
	vp     viewport.Model

	// new note
	titleInput textinput.Model
	tagsInput  textinput.Model
	bodyArea   textarea.Model
	newFocus   int // 0=title,1=tags,2=body
	editNote   *models.Note // non-nil when editing existing

	// status
	status     string
	statusTime time.Time
	err        error
	syncing    bool
}

func New() Model {
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

	return Model{
		searchInput: si,
		titleInput:  ti,
		tagsInput:   tags,
		bodyArea:    body,
	}
}

func Run() error {
	m := New()
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err := p.Run()
	return err
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(loadNotesCmd("", ""), tea.WindowSize())
}

func (m Model) activeFolder() string {
	if m.activeTab == 0 || m.activeTab >= len(m.folders)+1 {
		return ""
	}
	return m.folders[m.activeTab-1]
}

// ── Update ────────────────────────────────────────────────────────────────────

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.vp = viewport.New(msg.Width, m.bodyHeight())
		m.bodyArea.SetWidth(msg.Width - 4)
		m.bodyArea.SetHeight(m.height - 10)

	case notesLoadedMsg:
		m.notes = msg.notes
		m.folders = msg.folders
		if m.cursor >= len(m.notes) {
			m.cursor = max(0, len(m.notes)-1)
		}

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

	case errMsg:
		m.err = msg.err

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
		default:
			var cmd tea.Cmd
			m.searchInput, cmd = m.searchInput.Update(msg)
			return m, cmd
		}
		return m, nil
	}

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
	case "g":
		m.cursor = 0
	case "G":
		m.cursor = max(0, len(m.notes)-1)
	case "enter":
		if len(m.notes) > 0 {
			n := m.notes[m.cursor]
			m.detail = &n
			m.vp.SetContent(n.Body)
			m.vp.GotoTop()
			m.view = viewDetail
		}
	case "n":
		m.editNote = nil
		m.resetNew("")
		m.view = viewNew
	case "e":
		// edit current note
		if len(m.notes) > 0 {
			n := m.notes[m.cursor]
			m.editNote = &n
			m.resetNew(n.Title)
			m.titleInput.SetValue(n.Title)
			m.tagsInput.SetValue(strings.Join(n.Tags, ", "))
			m.bodyArea.SetValue(n.Body)
			m.view = viewNew
		}
	case "s":
		if !m.syncing {
			m.syncing = true
			m.setStatus("Syncing vault…")
			return m, doSyncCmd()
		}
	case "/":
		m.searching = true
		m.searchInput.Focus()
		m.searchInput.SetValue("")
	case "esc":
		if m.searchQ != "" {
			m.searchQ = ""
			m.searchInput.SetValue("")
			m.cursor = 0
			return m, loadNotesCmd("", m.activeFolder())
		}
	}
	return m, nil
}

func (m Model) updateDetail(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "esc":
		m.view = viewList
		m.detail = nil
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
	}
	var cmd tea.Cmd
	m.vp, cmd = m.vp.Update(msg)
	return m, cmd
}

func (m Model) updateNew(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+s":
		return m, writeNoteCmd(
			m.titleInput.Value(),
			m.bodyArea.Value(),
			m.tagsInput.Value(),
			m.activeFolder(),
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
	}
	return m, cmd
}

// ── View ──────────────────────────────────────────────────────────────────────

func (m Model) View() string {
	switch m.view {
	case viewDetail:
		return m.renderDetail()
	case viewNew:
		return m.renderNew()
	default:
		return m.renderList()
	}
}

func (m Model) renderList() string {
	var b strings.Builder

	// ── tab bar: All | Folder1 | Folder2 ──
	tabs := append([]string{"All"}, m.folders...)
	var parts []string
	for i, t := range tabs {
		if i == m.activeTab {
			parts = append(parts, styleTabActive.Render(t))
		} else {
			parts = append(parts, styleTabInact.Render(t))
		}
	}
	bar := strings.Join(parts, "")
	if m.syncing {
		bar += "  " + styleSyncing.Render("⟳ syncing…")
	}
	b.WriteString(bar + "\n")
	b.WriteString(styleDivider.Render(strings.Repeat("─", m.width)) + "\n")

	overhead := 3 // tab + divider + status
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
		b.WriteString("\n" + styleHelp.Render("  No notes — press s to sync vault, n to create") + "\n")
	} else {
		start := 0
		if m.cursor >= listH {
			start = m.cursor - listH + 1
		}
		end := min(len(m.notes), start+listH)
		for i := start; i < end; i++ {
			n := &m.notes[i]
			line := formatNoteRow(n, m.width)
			if i == m.cursor {
				line = styleSelected.Width(m.width).Render(line)
			}
			b.WriteString(line + "\n")
		}
	}

	// status bar
	countStr := ""
	if len(m.notes) > 0 {
		countStr = styleHelp.Render(fmt.Sprintf(" %d notes", len(m.notes)))
	}
	var bar2 string
	if m.err != nil {
		bar2 = styleErr.Render("✗ " + m.err.Error())
	} else if m.status != "" {
		bar2 = styleOK.Render("✓ " + m.status)
	} else {
		bar2 = styleHelp.Render("enter:open  n:new  e:edit  s:sync  /:search  tab:folder  q:quit")
	}
	pad := m.width - lipgloss.Width(bar2) - lipgloss.Width(countStr)
	if pad < 0 {
		pad = 0
	}
	b.WriteString("\n" + bar2 + strings.Repeat(" ", pad) + countStr)
	return b.String()
}

func (m Model) renderDetail() string {
	if m.detail == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString(styleBold.Render(m.detail.Title) + "\n")
	if m.detail.Folder != "" {
		b.WriteString(styleFolder.Render("📁 "+m.detail.Folder) + "  ")
	}
	if len(m.detail.Tags) > 0 {
		for _, t := range m.detail.Tags {
			b.WriteString(styleTag.Render("#"+t) + " ")
		}
		b.WriteString("\n")
	}
	b.WriteString(styleMuted.Render(m.detail.ModTime.Format("Mon, 02 Jan 2006 15:04")) + "\n")
	b.WriteString(styleDivider.Render(strings.Repeat("─", m.width)) + "\n")
	m.vp.Height = m.bodyHeight()
	b.WriteString(m.vp.View())
	pct := ""
	if m.vp.TotalLineCount() > m.vp.Height {
		pct = fmt.Sprintf(" %d%%", int(m.vp.ScrollPercent()*100))
	}
	b.WriteString("\n" + styleHelp.Render("esc:back  e:edit  ↑↓/jk:scroll  q:quit") + styleMuted.Render(pct))
	return b.String()
}

func (m Model) renderNew() string {
	title := "New Note"
	if m.editNote != nil {
		title = "Edit: " + m.editNote.Title
	}
	var b strings.Builder
	b.WriteString(styleHeader.Render(title) + "\n")
	b.WriteString(styleDivider.Render(strings.Repeat("─", m.width)) + "\n\n")

	focus := func(i int) string {
		if m.newFocus == i {
			return styleTabActive.Render("›")
		}
		return "  "
	}

	b.WriteString(focus(0) + " " + styleLabel.Render("Title:") + "  " + m.titleInput.View() + "\n")
	b.WriteString(focus(1) + " " + styleLabel.Render("Tags:") + "   " + m.tagsInput.View() + "\n\n")
	b.WriteString(focus(2) + " " + styleLabel.Render("Body:") + "\n")
	b.WriteString(m.bodyArea.View() + "\n\n")

	if m.err != nil {
		b.WriteString(styleErr.Render("✗ "+m.err.Error()) + "\n")
	} else {
		b.WriteString(styleHelp.Render("tab:next  ctrl+s:save  esc:cancel"))
	}
	return b.String()
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
		return notesLoadedMsg{notes: ns, folders: folders}
	}
}

func doSyncCmd() tea.Cmd {
	return func() tea.Msg {
		ns, err := notes.List(config.VaultPath())
		if err != nil {
			return syncDoneMsg{err: err}
		}
		s, err := store.New(config.DBPath())
		if err != nil {
			return syncDoneMsg{err: err}
		}
		defer s.Close()
		ctx := context.Background()
		_ = s.DeleteBySource(ctx, "obsidian")
		for i := range ns {
			_ = s.Upsert(ctx, &ns[i])
		}
		return syncDoneMsg{count: len(ns)}
	}
}

func writeNoteCmd(title, body, tagsStr, folder string) tea.Cmd {
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
		n, err := notes.Write(config.VaultPath(), title, body, tags, folder)
		if err != nil {
			return writeDoneMsg{err: err}
		}
		if s, serr := store.New(config.DBPath()); serr == nil {
			defer s.Close()
			_ = s.Upsert(context.Background(), n)
		}
		return writeDoneMsg{note: n}
	}
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func (m *Model) resetNew(title string) {
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
	h := m.height - 7
	if h < 5 {
		h = 5
	}
	return h
}

func formatNoteRow(n *models.Note, width int) string {
	date := smartDate(n.ModTime)
	title := n.Title
	folder := ""
	if n.Folder != "" {
		folder = styleFolder.Render(" " + n.Folder)
	}
	tags := ""
	if len(n.Tags) > 0 {
		tags = styleTag.Render(" #" + n.Tags[0])
	}

	// fixed columns: date(14) + title(rest)
	meta := folder + tags
	metaW := lipgloss.Width(meta)
	titleW := width - 16 - metaW
	if titleW < 10 {
		titleW = 10
	}
	if len(title) > titleW {
		title = title[:titleW-1] + "…"
	}
	return fmt.Sprintf("%-14s  %-*s%s", date, titleW, title, meta)
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
