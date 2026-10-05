package tui

import (
	"time"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	"github.com/aeon022/notectl/internal/models"
	"github.com/aeon022/notectl/internal/notes"
	"github.com/aeon022/notectl/internal/store"
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

// ── Messages ──────────────────────────────────────────────────────────────────

type notesLoadedMsg struct {
	notes         []models.Note
	folders       []string
	folderCounts  map[string]int
	accounts      []string
	accountCounts map[string]int
	// folderInfoByAccount is only populated in "All accounts" mode — see
	// loadNotesCmd — and feeds buildAccountAwareFolderTree's collision
	// detection.
	folderInfoByAccount map[string][]store.FolderInfo
}
type noteRestoredMsg struct {
	note *models.Note
	err  error
}
type syncDoneMsg struct {
	count         int
	mirrorPending int
	err           error
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
	// ":" command palette
	inPalette     bool
	paletteInput  textinput.Model
	paletteCursor int
	folders       []string // flat, full-path folders as stored on notes (e.g. "Projects/Git")
	folderCounts  map[string]int

	// Two-row notebook tabs — see tabs.go. topFolders/subFolders are
	// derived from folders each time it loads (buildFolderTree). tabCursor
	// indexes the flattened tab/shift+tab sequence (tabPositions): "All",
	// then each top-level notebook, then — only for a top-level notebook
	// that's been explicitly expanded (see expandedTops) — its own
	// children in order. tab/shift+tab used to walk straight into a
	// notebook's children the moment it became the active top-level
	// selection, with no way to just glance across the top-level row
	// without dragging every notebook's children along for the ride; row 2
	// only ever shows now if you asked for it with "right"/"l".
	topFolders []string            // top-level notebooks, row 1 (index 0 = "All")
	subFolders map[string][]string // top-level name -> its children's full paths, row 2
	tabCursor  int                 // index into tabPositions()
	tabScroll  int                 // first visible row-1 tab, kept in view by ensureTabVisible

	// expandedTops tracks which top-level notebooks currently show their
	// children in row 2 — keyed by topFolderKey(i), so two collision-split
	// tabs sharing a plain folder name (see topFolderAccounts) expand
	// independently rather than as one. Toggled by "right"/"l" (expand, or
	// step into the first child if already expanded) and "left"/"h"
	// (collapse, or step up to the parent if already on a child).
	// Deliberately not persisted — every launch starts fully collapsed, a
	// clean top-level overview rather than picking up wherever a previous
	// session's drill-down left off.
	expandedTops map[string]bool

	// topFolderAccounts is topFolders' parallel slice: "" for an ordinary
	// tab (scoped by whichever account the "["/"]" indicator has active),
	// or a specific account name when this tab is one half of a same-named
	// notebook collision (e.g. two Exchange accounts each with their own
	// "Notizen") — see buildAccountAwareFolderTree's doc comment. A bound
	// tab's own account overrides the global account filter for it
	// (effectiveAccount), which is what makes selecting it unambiguous
	// instead of merging both accounts' notes the way a single flat
	// "Notizen" tab used to. The tab's own label stays the plain folder
	// name regardless (see topFolderLabel) — which specific account it's
	// bound to is shown on renderAccountLine instead, not crammed into the
	// tab text itself.
	topFolderAccounts []string

	// hideEmptyNotebooks, toggled by a key (see keymap), drops any notebook
	// tab with zero notes in it from row 1/2 — mainly relevant now that
	// ListFolderInfo surfaces real-but-empty Apple Notes folders that used
	// to be entirely invisible (see migrate's doc comment on the folders
	// table): without this, every account's untouched default folders
	// would clutter the tab row. Persisted across restarts (see
	// persistedState) and cleared automatically the moment a note gets
	// written into any notebook — see the writeCmd success path — since a
	// notebook that just received a note is exactly the one case where
	// staying hidden would be actively confusing (you'd write a note and
	// then not be able to find its notebook).
	hideEmptyNotebooks bool

	// Account indicator (header, not a tab row) — Apple Notes only, e.g.
	// "iCloud", "FH Burgenland". Only shown when len(accounts) > 1: a
	// single account (or an obsidian vault, which has no account concept
	// at all) has nothing to disambiguate. "["/"]" cycle accounts
	// directly (see tabs.go's package doc for why this isn't a tab row).
	accounts      []string // distinct accounts (index 0 = "All accounts")
	accountCounts map[string]int
	accountCursor int // index into accounts, 0 = "All accounts"

	// pendingFolderRestore holds the persisted last-active folder path
	// (see uistate) until m.folders first loads, resolved to a tab
	// position and cleared then — same one-shot pattern as openPath below.
	// The active account is deliberately NOT restored the same way: every
	// launch starts on "All accounts" (accountCursor's zero value) so you
	// always land somewhere that shows everything, rather than silently
	// reopening scoped to whichever single account you happened to be on
	// when you last quit.
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
	sortByDate    bool    // true = mod_time desc (default), false = title asc
	paneRatio     float64 // two-pane left width ratio, list+preview view (default 0.38)
	editPaneRatio float64 // two-pane left width ratio, edit/new view (default 0.65) — editing wants far more room than a passive list preview does
	confirmID     string  // non-empty = waiting for delete confirmation

	// undo: "u" within undoWindow of a delete restores the deleted note —
	// same pattern and window taskctl uses for its own delete-undo.
	// statusTime doubles as its expiry clock (see the tea.KeyPressMsg case).
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
