package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/aeon022/missionctl-core/humanize"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"github.com/aeon022/missionctl-core/lastsync"
	"github.com/aeon022/missionctl-core/palette"
	"github.com/aeon022/notectl/internal/config"
	"github.com/aeon022/notectl/internal/models"
	"github.com/aeon022/notectl/internal/notes"
	"github.com/charmbracelet/x/ansi"
)

// ── Update ────────────────────────────────────────────────────────────────────

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		// -1, not msg.Height: every render path here fills its height
		// budget exactly (listHeight/preambleRows/helpBarHeight sum to
		// precisely m.height, by design, on every path), and View() never
		// ends in a trailing newline (the conventional, recommended way to
		// write it). That combination — output with exactly as many lines
		// as the terminal, no trailing newline — is a long-standing,
		// still-open bubbletea quirk (charmbracelet/bubbletea#304, aka
		// #1004): the renderer can fail to fully redraw or misplace the
		// last line specifically at that exact-height boundary, independent
		// of what the content actually is. Reserving one row of slack here
		// means notectl's own layout math never lands on that boundary,
		// regardless of terminal size — cheaper and more reliable than
		// trying to guarantee every render path never once produces a
		// flush that happens to be exactly msg.Height lines.
		m.height = msg.Height - 1
		if m.height < 1 {
			m.height = 1
		}
		m.vp = viewport.New(viewport.WithWidth(msg.Width), viewport.WithHeight(m.bodyHeight()))
		m.pvp = viewport.New(viewport.WithWidth(m.pvpWidth()), viewport.WithHeight(m.height-3))
		m.bodyArea.SetWidth(m.editorBodyWidth())
		m.bodyArea.SetHeight(m.height - 11)
		m.ensureTabVisible()

	case tea.FocusMsg:
		// Back in the window: refresh the list from the local DB (not a
		// sync) — but only while just browsing, never under input or a
		// pending confirmation, and not more often than every 5s.
		if m.browsingIdle() && time.Since(m.lastLoad) > focusReloadAfter {
			m.lastLoad = time.Now() // debounce focus flicker; no loading flag, so the list never blanks
			return m, loadNotesCmd(m.effectiveAccount(), m.activeFolder(), m.activeAccount())
		}
		return m, nil

	case notesLoadedMsg:
		m.loading = false
		m.lastLoad = time.Now()
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
		if msg.folderInfoByAccount != nil {
			m.topFolders, m.topFolderAccounts, m.subFolders =
				buildAccountAwareFolderTree(msg.folders, msg.folderCounts, msg.folderInfoByAccount, m.hideEmptyNotebooks)
		} else {
			tops, kids := buildFolderTree(filterEmptyFolders(msg.folders, msg.folderCounts, m.hideEmptyNotebooks))
			m.topFolders = tops
			m.topFolderAccounts = make([]string, len(tops))
			m.subFolders = kids
		}
		m.accounts = msg.accounts
		if msg.accountCounts != nil {
			m.accountCounts = msg.accountCounts
		}
		if m.pendingFolderRestore != "" {
			restore := m.pendingFolderRestore
			m.pendingFolderRestore = ""
			// Children are only reachable via tabPositions/resolveTabCursor
			// once their parent is expanded (see expandedTops) — restoring
			// into one has to expand its parent first, or the lookup below
			// finds nothing and silently falls back to "All". Keyed by the
			// plain top name, matching a non-collision-bound restore target
			// (a persisted path can't record which of two collision-split
			// tabs it meant, so this doesn't attempt to disambiguate that).
			if i := strings.IndexByte(restore, '/'); i >= 0 && m.expandedTops != nil {
				m.expandedTops[restore[:i]] = true
			} else if i >= 0 {
				m.expandedTops = map[string]bool{restore[:i]: true}
			}
			if cursor, ok := m.resolveTabCursor(restore); ok {
				m.tabCursor = cursor
				m.ensureTabVisible()
				return m, tea.Batch(loadNotesCmd(m.effectiveAccount(), restore, m.activeAccount()), tea.ClearScreen)
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
					return m, tea.Batch(cmd, tea.ClearScreen)
				}
			}
		}
		if !found && m.cursor >= len(m.notes) {
			m.cursor = max(0, len(m.notes)-1)
		}
		m = m.applySortOrder()
		var pvCmd tea.Cmd
		m, pvCmd = m.refreshPreview()
		// tea.ClearScreen, not just pvCmd: a reload here can change the
		// notebook tree (account line, row 2's presence) and so the total
		// number of chrome lines above the list — a shape change nothing
		// else forces a full repaint for (unlike a resize, which Bubble
		// Tea already repaints on its own). Without it, the renderer's
		// line-by-line diff can skip a row whose content coincidentally
		// still matches what used to be at that same index before the
		// shape changed, leaving it stale while everything around it
		// updates — reported live as a note's old title/preview lingering
		// above the new one, or the header/divider vanishing right after a
		// sync landed.
		return m, tea.Batch(pvCmd, tea.ClearScreen)

	case lastSyncedLoadedMsg:
		m.lastSynced = msg.t

	case syncDoneMsg:
		m.syncing = false
		if msg.err != nil {
			m.err = msg.err
		} else {
			status := fmt.Sprintf("Synced %d notes", msg.count)
			if len(config.SyncSources()) > 1 {
				// Raw combined scan count, not the note count you'll see
				// browsing — with two sources, a mirrored note is counted
				// once per side here, while the "All accounts" list dedupes
				// it down to its Apple row (see excludeMirroredObsidianSQL
				// in internal/store/sqlite.go).
				status += " (combined raw scan — mirrored notes count twice; 'All' shows the deduped total)"
			}
			if msg.mirrorPending > 0 {
				status += fmt.Sprintf(" (%d mirror deletion(s) pending — run 'notectl sync --apply-deletes')", msg.mirrorPending)
			}
			m.setStatus(status)
			m.lastSynced = time.Now()
			_ = lastsync.Save(config.LastSyncedPath(), m.lastSynced)
			return m, loadNotesCmd(m.effectiveAccount(), m.activeFolder(), m.activeAccount())
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
			// A note just landed somewhere — if "hide empty notebooks" was
			// on, that notebook (or one just like it) is exactly the case
			// where staying hidden would be actively confusing: you'd write
			// a note and then not be able to find the notebook it went
			// into. Clearing it here rather than leaving it on and just
			// revealing the one affected notebook matches what was asked
			// for — the whole filter turns itself off, not just a
			// carve-out for this one note.
			if m.hideEmptyNotebooks {
				m.hideEmptyNotebooks = false
				m.saveUIState()
			}
			return m, loadNotesCmd(m.effectiveAccount(), m.activeFolder(), m.activeAccount())
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
			return m, loadNotesCmd(m.effectiveAccount(), m.activeFolder(), m.activeAccount())
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
			return m, loadNotesCmd(m.effectiveAccount(), m.activeFolder(), m.activeAccount())
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
					cachedNote = &m.notes[i]
					break
				}
			}
			m.setNoteBody(msg.id, body)
			// update detail view if open
			if m.detail != nil && m.detail.ID == msg.id {
				m.detail.Body = body
				m.detailBlocks = blocks
				content, visualCursor := renderDetailBody(body, m.detailLineCursor, m.detailBodyWidth())
				// Adjust offset if cursor went off screen due to length change
				if visualCursor >= m.detailYOffset+m.vp.Height() {
					m.detailYOffset = visualCursor - m.vp.Height() + 1
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

	case tea.MouseWheelMsg:
		switch msg.Button {
		case tea.MouseWheelUp:
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
		case tea.MouseWheelDown:
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
		}

	case tea.MouseClickMsg:
		if msg.Button != tea.MouseLeft {
			return m, nil
		}
		if m.view == viewNew {
			return m.handleEditorClick(msg.X, msg.Y), nil
		}
		if m.view != viewList {
			return m, nil
		}
		if row, i := m.tabHitTest(msg.X, msg.Y); row >= 0 {
			var newCursor int
			if row == 0 {
				newCursor = m.cursorFor(i, -1)
			} else {
				newCursor = m.cursorFor(m.currentPos().top, i)
			}
			if newCursor != m.tabCursor {
				m.tabCursor = newCursor
				m.ensureTabVisible()
				m.cursor = 0
				m.saveUIState()
				return m, loadNotesCmd(m.effectiveAccount(), m.activeFolder(), m.activeAccount())
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

	case tea.MouseMotionMsg:
		if m.view == viewList {
			m.hoverRow = m.rowHitTest(msg.X, msg.Y)
		}

	case spinner.TickMsg:
		if m.syncing || m.loading {
			var cmd tea.Cmd
			m.sp, cmd = m.sp.Update(msg)
			return m, cmd
		}
		return m, nil

	case tea.KeyPressMsg:
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

func (m Model) updateList(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.inPalette {
		closePalette := func(mm Model) Model {
			mm.inPalette = false
			mm.paletteInput.Blur()
			mm.paletteInput.SetValue("")
			mm.paletteCursor = 0
			return mm
		}
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "esc":
			return closePalette(m), nil
		case "up", "ctrl+p":
			if m.paletteCursor > 0 {
				m.paletteCursor--
			}
			return m, nil
		case "down", "ctrl+n":
			matches := palette.Match(paletteCommands, m.paletteInput.Value())
			if m.paletteCursor < len(matches)-1 {
				m.paletteCursor++
			}
			return m, nil
		case "enter":
			matches := palette.Match(paletteCommands, m.paletteInput.Value())
			if len(matches) == 0 {
				return closePalette(m), nil
			}
			if m.paletteCursor >= len(matches) {
				m.paletteCursor = len(matches) - 1
			}
			chosen := matches[m.paletteCursor]
			m = closePalette(m)
			replay := tea.KeyPressMsg{Text: chosen.Key, Code: []rune(chosen.Key)[0]}
			return m.updateList(replay)
		}
		var cmd tea.Cmd
		m.paletteInput, cmd = m.paletteInput.Update(msg)
		m.paletteCursor = 0
		return m, cmd
	}

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
		if n := len(m.tabPositions()); n > 0 {
			m.tabCursor = (m.tabCursor + 1) % n
		}
		m.ensureTabVisible()
		m.cursor = 0
		m.saveUIState()
		return m, loadNotesCmd(m.effectiveAccount(), m.activeFolder(), m.activeAccount())
	case "shift+tab":
		if n := len(m.tabPositions()); n > 0 {
			m.tabCursor = (m.tabCursor - 1 + n) % n
		}
		m.ensureTabVisible()
		m.cursor = 0
		m.saveUIState()
		return m, loadNotesCmd(m.effectiveAccount(), m.activeFolder(), m.activeAccount())
	case "right", "l":
		// On a collapsed top-level notebook with children: expand it (row 2
		// appears, cursor stays put). On one already expanded: step onto
		// its first child instead — mirrors the common file-tree
		// convention (VS Code, Finder) of "right" meaning "reveal, then
		// descend" rather than doing both at once.
		pos := m.currentPos()
		if pos.top == 0 || pos.top > len(m.topFolders) || pos.sub >= 0 || !m.hasChildren(pos.top-1) {
			return m, nil
		}
		if !m.isExpanded(pos.top - 1) {
			m.setExpanded(pos.top-1, true)
			m.ensureTabVisible()
			return m, nil
		}
		m.tabCursor = m.cursorFor(pos.top, 0)
		m.ensureTabVisible()
		m.cursor = 0
		m.saveUIState()
		return m, loadNotesCmd(m.effectiveAccount(), m.activeFolder(), m.activeAccount())
	case "left", "h":
		// On a child: step back up to its parent notebook. Otherwise, on an
		// expanded top-level notebook: collapse it. Mirrors "right"/"l".
		pos := m.currentPos()
		if pos.top == 0 || pos.top > len(m.topFolders) {
			return m, nil
		}
		if pos.sub >= 0 {
			m.tabCursor = m.cursorFor(pos.top, -1)
			m.ensureTabVisible()
			m.cursor = 0
			m.saveUIState()
			return m, loadNotesCmd(m.effectiveAccount(), m.activeFolder(), m.activeAccount())
		}
		if m.isExpanded(pos.top - 1) {
			m.setExpanded(pos.top-1, false)
			m.ensureTabVisible()
		}
		return m, nil
	case "[", "]":
		// Cycle the active account directly — immediate, visible effect
		// (header indicator changes, row 1 reloads scoped to it), not a
		// focus mode tab/shift+tab would then need to be redirected into.
		// See tabs.go's package doc for why this replaced an earlier
		// account tab row.
		if m.hasMultipleAccounts() {
			n := len(m.accounts) + 1
			if msg.String() == "[" {
				m.accountCursor = (m.accountCursor - 1 + n) % n
			} else {
				m.accountCursor = (m.accountCursor + 1) % n
			}
			m.tabCursor = 0
			m.ensureTabVisible()
			m.cursor = 0
			m.saveUIState()
			return m, loadNotesCmd(m.effectiveAccount(), m.activeFolder(), m.activeAccount())
		}
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
				m.setStatus(fmt.Sprintf("Delete \"%s\"?  d:confirm  esc:cancel", humanize.Truncate(n.Title, 30)))
				return m, nil
			}
			// confirmed
			m.confirmID = ""
			m.dropNote(n.ID)
			if m.cursor >= len(m.notes) {
				m.cursor = max(0, len(m.notes)-1)
			}
			m.lastDeleted = &n
			m.setStatus("Deleted: " + n.Title + " — press u to undo")
			ref := n.Path
			if config.Source() == config.SourceApple {
				ref = n.ID
			}
			return m, deleteNoteCmd(n.ID, ref, n.Title)
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
	case "H":
		// A full reload (rather than filtering m.topFolders in place) since
		// the hidden/shown set depends on data (folderCounts,
		// folderInfoByAccount) already loaded — cheapest correct way to
		// re-derive the tree is the same path a sync or tab switch already
		// takes. saveUIState persists the new value immediately (not just
		// on quit) — see hideEmptyNotebooks' doc comment on why it's
		// persisted at all — so a crash right after toggling it doesn't
		// silently lose the change.
		m.hideEmptyNotebooks = !m.hideEmptyNotebooks
		m.saveUIState()
		if m.hideEmptyNotebooks {
			m.setStatus("Hiding empty notebooks")
		} else {
			m.setStatus("Showing empty notebooks")
		}
		return m, loadNotesCmd(m.effectiveAccount(), m.activeFolder(), m.activeAccount())
	case "y":
		if len(m.notes) > 0 {
			m.setStatus("Copied: " + humanize.Truncate(m.notes[m.cursor].Title, 30))
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
	case ":":
		m.inPalette = true
		m.paletteCursor = 0
		m.paletteInput.SetValue("")
		return m, m.paletteInput.Focus()
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

func (m Model) updateDetail(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
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
				m.setStatus(fmt.Sprintf("Delete \"%s\"?  d:confirm  esc:cancel", humanize.Truncate(m.detail.Title, 30)))
				return m, nil
			}
			m.confirmID = ""
			ref := m.detail.Path
			if config.Source() == config.SourceApple {
				ref = m.detail.ID
			}
			n := *m.detail
			id, path, title := n.ID, ref, n.Title
			m.dropNote(id)
			if m.cursor >= len(m.notes) {
				m.cursor = max(0, len(m.notes)-1)
			}
			m.detail = nil
			m.detailLineCursor = 0
			m.detailYOffset = 0
			m.view = viewList
			m.lastDeleted = &n
			m.setStatus("Deleted: " + title + " — press u to undo")
			return m, deleteNoteCmd(id, path, title)
		}

	case "j", "down":
		if m.detail != nil {
			lines := strings.Split(m.detail.Body, "\n")
			if next := nextNonBlankLine(lines, m.detailLineCursor, 1); next != m.detailLineCursor {
				m.detailLineCursor = next
				content, visualCursor := renderDetailBody(m.detail.Body, m.detailLineCursor, m.detailBodyWidth())
				if visualCursor >= m.detailYOffset+m.vp.Height() {
					m.detailYOffset = visualCursor - m.vp.Height() + 1
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
			m.detailLineCursor = min(len(lines)-1, m.detailLineCursor+max(1, m.vp.Height()/2))
			m = m.syncDetailViewport()
		}

	case "pgup", "ctrl+b":
		if m.detail != nil {
			m.detailLineCursor = max(0, m.detailLineCursor-max(1, m.vp.Height()/2))
			m = m.syncDetailViewport()
		}

	case "space":
		// toggle ☐ ↔ ☑ on current line, write back to Apple Notes
		if m.detail != nil {
			lines := strings.Split(m.detail.Body, "\n")
			if m.detailLineCursor < len(lines) {
				toggled := toggleCheckboxLine(lines[m.detailLineCursor])
				if toggled != lines[m.detailLineCursor] {
					lines[m.detailLineCursor] = toggled
					newBody := strings.Join(lines, "\n")
					m.detail.Body = newBody
					m.setNoteBody(m.detail.ID, newBody)
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
	} else if visualCursor >= m.detailYOffset+m.vp.Height() {
		m.detailYOffset = visualCursor - m.vp.Height() + 1
	}
	if m.detailYOffset < 0 {
		m.detailYOffset = 0
	}

	m.vp.SetContent(content)
	m.vp.SetYOffset(m.detailYOffset)
	return m
}

func (m Model) updateNew(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
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

func (m Model) updateTags(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
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
func (m Model) updateGraph(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
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

func (m Model) updateSettings(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
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
		wrapped := ansi.Hardwrap(formatted, width, true)
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
