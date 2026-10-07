package tui

import (
	"image/color"
	"os"
	"time"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/aeon022/missionctl-core/lastsync"
	"github.com/aeon022/missionctl-core/palette"
	"github.com/aeon022/missionctl-core/theme"
	"github.com/aeon022/missionctl-core/uistate"
	"github.com/aeon022/notectl/internal/config"
)

// ── Styles ────────────────────────────────────────────────────────────────────

// isDarkBG is resolved once at package load — lipgloss v2 dropped
// AdaptiveColor (a static Light/Dark struct lipgloss itself resolved
// internally) in favor of a LightDarkFunc meant to be re-resolved against a
// live tea.BackgroundColorMsg on every render. This app builds all its
// styles once, at package load, same as before v2 — adaptiveColor below is
// the one-shot equivalent of what AdaptiveColor did implicitly.
var isDarkBG = lipgloss.HasDarkBackground(os.Stdin, os.Stdout)

// adaptiveColor picks light or dark once, at package load — see isDarkBG.
func adaptiveColor(light, dark string) color.Color {
	if isDarkBG {
		return lipgloss.Color(dark)
	}
	return lipgloss.Color(light)
}

var (
	// Shared across the suite via missionctl-core/theme.
	colorBlue   = theme.BlueV2
	colorGreen  = theme.GreenV2
	colorRed    = theme.RedV2
	colorMuted  = theme.MutedV2
	colorSubtle = theme.SubtleV2
	colorAmber  = theme.AmberV2
	colorTabBg  = adaptiveColor("252", "235")

	styleHeader   = lipgloss.NewStyle().Bold(true).Foreground(colorBlue)
	styleDivider  = lipgloss.NewStyle().Foreground(colorSubtle)
	styleHelp     = lipgloss.NewStyle().Foreground(colorMuted)
	styleErr      = lipgloss.NewStyle().Foreground(colorRed)
	styleOK       = lipgloss.NewStyle().Foreground(colorGreen)
	styleMuted    = lipgloss.NewStyle().Foreground(colorMuted)
	styleBold     = lipgloss.NewStyle().Bold(true)
	styleSelected = lipgloss.NewStyle().
			Background(theme.SelectedBgV2).
			Foreground(theme.SelectedFgV2).
			Bold(true)
	styleTag     = lipgloss.NewStyle().Foreground(adaptiveColor("33", "75"))
	styleFolder  = lipgloss.NewStyle().Foreground(adaptiveColor("136", "178"))
	styleLabel   = lipgloss.NewStyle().Foreground(colorBlue).Width(9)
	styleSyncing = lipgloss.NewStyle().Foreground(adaptiveColor("214", "220"))

	styleTabActive = lipgloss.NewStyle().Bold(true).
			Foreground(theme.OnAccentV2).
			Background(colorBlue).
			Padding(0, 1)
	styleTabInact = lipgloss.NewStyle().
			Foreground(adaptiveColor("237", "252")).
			Background(colorTabBg).
			Padding(0, 1)
	// styleTabActiveDim marks the row-1 tab whose sub-notebook is active in
	// row 2 — still "selected" (bold, tinted) but visually receded since one
	// of its children (row 2) is the actual current focus.
	styleTabActiveDim = lipgloss.NewStyle().Bold(true).
				Foreground(colorBlue).
				Background(colorTabBg).
				Padding(0, 1)
	// Row 2 (sub-notebooks) is deliberately plain text, not filled pills —
	// its contents change with every parent and read better as a
	// lightweight breadcrumb than another row of buttons.
	styleTabParentRef = lipgloss.NewStyle().Bold(true).Foreground(colorBlue)
	styleSubInact     = lipgloss.NewStyle().Foreground(colorMuted)
	styleSubActive    = lipgloss.NewStyle().Bold(true).Underline(true).Foreground(colorBlue)

	// markdown
	styleMDH1    = lipgloss.NewStyle().Bold(true).Foreground(colorBlue)
	styleMDH2    = lipgloss.NewStyle().Bold(true).Foreground(adaptiveColor("26", "39"))
	styleMDH3    = lipgloss.NewStyle().Bold(true)
	styleMDQuote = lipgloss.NewStyle().Foreground(colorMuted)
	styleMDCode  = lipgloss.NewStyle().Foreground(adaptiveColor("130", "215"))
	styleMDBold  = lipgloss.NewStyle().Bold(true)
	styleStrike  = lipgloss.NewStyle().Strikethrough(true).Foreground(colorMuted)

	// date age colors — amber for today (matches mailctl styleToday), fading to subtle
	styleDateToday = lipgloss.NewStyle().Foreground(adaptiveColor("214", "220")).Bold(true)
	styleDateWeek  = lipgloss.NewStyle().Foreground(adaptiveColor("243", "246"))
	styleDateMonth = lipgloss.NewStyle().Foreground(colorMuted)
	styleDateOld   = lipgloss.NewStyle().Foreground(colorMuted) // Subtle is too faint for the date column
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
	{config.SourceJoplin, "Joplin", "via Joplin's local Data API — needs Joplin running, Web Clipper enabled"},
}

// ── command palette (":") ────────────────────────────────────────────────────
//
// Types out full words instead of memorizing single-key shortcuts. Reuses
// the exact same key handling every shortcut already goes through
// (updateList) by replaying the mapped keypress, so behavior is guaranteed
// identical to typing the key directly. Matching logic lives in
// missionctl-core/palette (shared across the suite); this list is
// notectl-specific.
var paletteCommands = []palette.Command{
	{Name: "new", Desc: "New note", Key: "n"},
	{Name: "edit", Desc: "Edit selected note", Key: "e"},
	{Name: "delete", Desc: "Delete note (asks to confirm)", Key: "d"},
	{Name: "open", Desc: "Open in external app", Key: "o"},
	{Name: "copy", Desc: "Copy title to clipboard", Key: "y"},
	{Name: "undo", Desc: "Undo last delete", Key: "u"},
	{Name: "sort", Desc: "Toggle sort (date / title A–Z)", Key: "S"},
	{Name: "hideempty", Desc: "Toggle hiding empty notebooks", Key: "H"},
	{Name: "expand", Desc: "Expand notebook / step into its first child", Key: "l"},
	{Name: "collapse", Desc: "Collapse notebook / step up to its parent", Key: "h"},
	{Name: "settings", Desc: "Settings (vault path, source)", Key: "p"},
	{Name: "sync", Desc: "Sync", Key: "s"},
	{Name: "search", Desc: "Search notes", Key: "/"},
	{Name: "tags", Desc: "Browse tags", Key: "t"},
	{Name: "graph", Desc: "Link graph (browse [[wiki-links]])", Key: "L"},
	{Name: "help", Desc: "Show help", Key: "?"},
	{Name: "quit", Desc: "Quit notectl", Key: "q"},
}

func New(openPath string) Model {
	sp := spinner.New()
	sp.Spinner = spinner.MiniDot
	sp.Style = styleSyncing

	si := textinput.New()
	si.Placeholder = "search notes…"
	si.CharLimit = 200
	si.SetWidth(40) // v2: width 0 clips the placeholder to 1 char

	pi := textinput.New()
	pi.Placeholder = "command…"
	pi.CharLimit = 40
	pi.SetWidth(40) // v2: width 0 clips the placeholder to 1 char

	ti := textinput.New()
	ti.Placeholder = "Note title"
	ti.CharLimit = 200
	ti.Focus()
	ti.SetWidth(40) // v2: width 0 clips the placeholder to 1 char

	tags := textinput.New()
	tags.Placeholder = "tag1, tag2 (optional)"
	tags.CharLimit = 200
	tags.SetWidth(40) // v2: width 0 clips the placeholder to 1 char

	body := textarea.New()
	body.Placeholder = "Write your note here…"
	body.ShowLineNumbers = false

	vi := textinput.New()
	vi.Placeholder = "~/Documents/MyVault"
	vi.CharLimit = 500
	vi.SetValue(config.VaultPathRaw())
	vi.SetWidth(40) // v2: width 0 clips the placeholder to 1 char

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
		paletteInput:         pi,
		titleInput:           ti,
		tagsInput:            tags,
		bodyArea:             body,
		vaultInput:           vi,
		sourceIdx:            srcIdx,
		sortByDate:           true,
		paneRatio:            0.38,
		editPaneRatio:        0.65,
		loading:              true,
		hoverRow:             -1,
		lastClickRow:         -1,
		openPath:             openPath,
		pendingFolderRestore: state.LastFolder,
		hideEmptyNotebooks:   state.HideEmptyNotebooks,
	}
}

// persistedState is what New() restores from and saveUIState saves to — see
// missionctl-core/uistate. Deliberately has no LastAccount: the active
// account always starts fresh at "All accounts" on launch, see
// pendingFolderRestore's doc comment. HideEmptyNotebooks IS persisted,
// unlike that — it's a display preference ("I don't want clutter"), not
// browsing position, so there's no equivalent reason to reset it on launch.
type persistedState struct {
	LastFolder         string `json:"last_folder"`
	HideEmptyNotebooks bool   `json:"hide_empty_notebooks"`
}

func (m Model) saveUIState() {
	_ = uistate.Save(config.UIStatePath(), persistedState{
		LastFolder:         m.activeFolder(),
		HideEmptyNotebooks: m.hideEmptyNotebooks,
	})
}

// motionThrottleFilter drops MouseActionMotion messages that arrive less
// than 16ms after the last one that was let through, capping how often a
// mouse-motion event alone can trigger a full render (~60/s) — everything
// else (keys, clicks, resize, all the async load/sync messages) always
// passes through untouched. Bubble Tea calls model.View() and writes a
// render for every single message it receives regardless of whether Update
// actually changed anything (see its eventLoop), and WithMouseAllMotion
// reports every pixel of movement, not just cell-boundary crossings — on a
// fast trackpad that's dozens of renders a second from hovering alone, on
// top of whatever the mouse happened to be doing at the time. Confirmed
// live: adding an artificial per-render delay (temporary debug file
// logging on every View() call) made the reported corruption — a
// tab/header row duplicating and staying stuck — stop reproducing, which
// points at render-rate/timing rather than anything in the rendered
// content itself (already-generated frames logged during a live repro
// were byte-for-byte correct, single header, single footer, right line
// count throughout). This is the deliberate, permanent version of that
// same effect: no logging or working-model dependency, no per-message
// state that needs to live on Model, just backpressure at the one message
// type that can flood without bound. A closure-captured timestamp is safe
// here without extra locking — Bubble Tea's event loop calls this filter
// synchronously from a single goroutine, one message at a time.
func motionThrottleFilter() func(tea.Model, tea.Msg) tea.Msg {
	var lastMotion time.Time
	return func(_ tea.Model, msg tea.Msg) tea.Msg {
		if _, ok := msg.(tea.MouseMotionMsg); !ok {
			return msg
		}
		now := time.Now()
		if now.Sub(lastMotion) < 16*time.Millisecond {
			return nil
		}
		lastMotion = now
		return msg
	}
}

func Run(openPath string) error {
	m := New(openPath)
	// WithFPS(30), not the 60 default: confirmed live that the reported
	// corruption (a tab/header row duplicating and staying stuck) stops
	// reproducing whenever *anything* slows down how fast frames reach the
	// terminal — a debug-logging delay on every View() call, then running
	// through `script`'s pty relay, both masked it with no notectl code
	// changes at all. That points at the real terminal not keeping up with
	// how fast Bubble Tea's default 60fps ticker can write full-screen,
	// heavily-styled frames — not at anything wrong with the frames
	// themselves (already confirmed byte-for-byte correct throughout a
	// live repro via the debug log). A static notes browser has nothing
	// that benefits from 60fps in the first place; halving it gives the
	// terminal the same breathing room those workarounds did, permanently,
	// without depending on external wrapping.
	p := tea.NewProgram(m, tea.WithFilter(motionThrottleFilter()), tea.WithFPS(30))
	_, err := p.Run()
	return err
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(loadNotesCmd("", "", ""), doSyncCmd(), tea.RequestWindowSize, m.sp.Tick, loadLastSyncedCmd())
}

type lastSyncedLoadedMsg struct{ t time.Time }

func loadLastSyncedCmd() tea.Cmd {
	return func() tea.Msg {
		t, _ := lastsync.Load(config.LastSyncedPath())
		return lastSyncedLoadedMsg{t: t}
	}
}

func (m Model) activeFolder() string {
	pos := m.currentPos()
	if pos.top == 0 || pos.top > len(m.topFolders) {
		return ""
	}
	top := m.topFolders[pos.top-1]
	if kids := m.subFolders[top]; pos.sub >= 0 && pos.sub < len(kids) {
		return kids[pos.sub]
	}
	return top
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

// editorLeftWidth is renderNew's left (editable) pane width — its own,
// wider ratio than leftWidth's list+preview split, since actively typing
// needs far more room than glancing at a passive preview does.
func (m Model) editorLeftWidth() int {
	if !m.isTwoPane() {
		return m.width
	}
	r := m.editPaneRatio
	if r <= 0 {
		r = 0.65
	}
	return min(int(float64(m.width)*r), m.width-20)
}
func (m Model) editorPvpWidth() int { return m.width - m.editorLeftWidth() - 1 }

// editorBodyWidth is the textarea width in the new/edit view — the left pane
// when the live preview is shown, full width otherwise.
func (m Model) editorBodyWidth() int {
	if m.isTwoPane() {
		return m.editorLeftWidth() - 4
	}
	return m.width - 4
}
