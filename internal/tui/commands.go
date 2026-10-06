package tui

import (
	"context"
	"fmt"
	"github.com/aeon022/notectl/internal/actlog"
	"os/exec"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/aeon022/notectl/internal/config"
	"github.com/aeon022/notectl/internal/mirror"
	"github.com/aeon022/notectl/internal/models"
	"github.com/aeon022/notectl/internal/notes"
	"github.com/aeon022/notectl/internal/store"
	"github.com/aeon022/notectl/internal/syncdispatch"
	"github.com/sahilm/fuzzy"
)

// ── Commands ──────────────────────────────────────────────────────────────────

// loadNotesCmd fetches notes for folder, unfiltered by search text —
// search is applied client-side (filterNotes) over the result, live as the
// user types, rather than round-tripping to SQLite on EVERY keystroke (the
// previous behavior). Store.Filter.Query / the SQL LIKE path still exists
// and is still used by `notectl search` and the MCP search tool, just not
// from here anymore.
// loadNotesCmd fetches the note list, scoped to account (the note filter —
// effectiveAccount(), which can be a collision-split tab's own bound
// account even while browsing "All accounts", see topFolderAccounts) and
// folder, plus the data needed to rebuild the row-1/2 tab tree.
//
// globalAccount is deliberately a SEPARATE parameter from account, not
// reused from it — it's the "["/"]" filter's own state (activeAccount()),
// and it alone decides which tab-tree shape gets rebuilt below. An earlier
// version used account for both, which conflated "which account filters
// the notes I'm looking at" with "which account the user explicitly
// selected": landing on a collision-split tab (e.g. one of two "Notizen"
// tabs) set account to that tab's own bound account, and since that made
// account != "", the tree got rebuilt from that ONE account's own folders
// only — the entire row-1 tab bar collapsed down to whatever that single
// account happened to have (confirmed live: "All accounts (3)" still
// showing in the header while row 1 silently shrank to just "All" +
// "Notizen"). Keying the tree choice on globalAccount instead means the
// tab bar stays the full "All accounts" tree the whole time you're
// browsing within it, exactly as long as you haven't explicitly cycled
// "["/"]" to a specific account yourself.
func loadNotesCmd(account, folder, globalAccount string) tea.Cmd {
	return func() tea.Msg {
		s, err := store.New(config.DBPath(), config.Shared())
		if err != nil {
			return errMsg{err}
		}
		defer s.Close()
		ctx := context.Background()
		ns, err := s.List(ctx, store.Filter{Account: account, Folder: folder, Limit: 500})
		if err != nil {
			return errMsg{err}
		}

		var infos []store.FolderInfo
		if globalAccount != "" {
			infos, _ = s.ListFolderInfoByAccount(ctx, globalAccount)
		} else {
			infos, _ = s.ListFolderInfo(ctx)
		}
		folders, counts := splitFolderInfo(infos)
		// ListFolderInfo/ByAccount deliberately never returns a Folder=""
		// row (it's not a real notebook), but the "All" tab's own count
		// still needs the grand total under that key — CountByFolder
		// already computes it internally, just cheap enough to ask for
		// again here rather than changing FolderInfo's shape for one int.
		if plain, perr := s.CountByFolder(ctx, globalAccount); perr == nil {
			counts[""] = plain[""]
		}

		accounts, _ := s.ListAccounts(ctx)
		accountCounts, _ := s.CountByAccount(ctx)

		// Per-account folder breakdown — only needed in "All accounts" mode
		// to detect a same-named top-level notebook colliding across more
		// than one account (see buildAccountAwareFolderTree). A specific
		// account is already unambiguous on its own, so skip the extra
		// queries there.
		var byAccount map[string][]store.FolderInfo
		if globalAccount == "" && len(accounts) > 1 {
			byAccount = map[string][]store.FolderInfo{}
			for _, a := range accounts {
				ai, _ := s.ListFolderInfoByAccount(ctx, a)
				byAccount[a] = ai
			}
		}

		return notesLoadedMsg{
			notes: ns, folders: folders, folderCounts: counts,
			accounts: accounts, accountCounts: accountCounts,
			folderInfoByAccount: byAccount,
		}
	}
}

// splitFolderInfo separates a FolderInfo slice (as returned by
// store.ListFolderInfo/ListFolderInfoByAccount) into the flat folder-path
// list and per-path count map the rest of the tab-building code expects.
func splitFolderInfo(infos []store.FolderInfo) ([]string, map[string]int) {
	folders := make([]string, 0, len(infos))
	counts := make(map[string]int, len(infos))
	for _, fi := range infos {
		folders = append(folders, fi.Folder)
		counts[fi.Folder] = fi.Count
	}
	return folders, counts
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

// doSyncCmd syncs every source configured via config.SyncSources() (just
// the active Source() by default). One source failing doesn't block the
// rest — its error is still surfaced, but sources that succeeded are kept.
func doSyncCmd() tea.Cmd {
	return func() tea.Msg {
		s, err := store.New(config.DBPath(), config.Shared())
		if err != nil {
			return syncDoneMsg{err: err}
		}
		defer s.Close()
		ctx := context.Background()

		params := syncdispatch.ParamsFromConfig()
		var total int
		var lastErr error
		for _, src := range config.SyncSources() {
			ns, err := syncdispatch.List(src, params)
			if err != nil {
				lastErr = err
				continue
			}
			_ = s.DeleteBySource(ctx, syncdispatch.SourceKey(src))
			for i := range ns {
				_ = s.Upsert(ctx, &ns[i])
			}
			if byAccount, ferr := syncdispatch.SyncFolders(src); ferr == nil && byAccount != nil {
				_ = s.ReplaceFolders(ctx, syncdispatch.SourceKey(src), byAccount)
			}
			total += len(ns)
		}

		mirrorPending := 0
		if config.MirrorEnabled() {
			if !config.MirrorSourcesConfigured() {
				// Same skip the CLI does: the flag alone isn't enough,
				// sync_sources has to actually cover both sides.
				if lastErr == nil {
					lastErr = fmt.Errorf("mirror_apple_obsidian is set, but sync_sources doesn't include both apple and obsidian — mirror sync skipped")
				}
			} else if report, mErr := mirror.Sync(ctx, s, params.VaultPath, params.AppleFolder, params.ExcludeFolders); mErr != nil {
				lastErr = mErr
			} else {
				total += report.Created + report.Updated
				mirrorPending = report.PendingDeletes
			}
		}

		return syncDoneMsg{count: total, mirrorPending: mirrorPending, err: lastErr}
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

func deleteNoteCmd(id, relPath, title string) tea.Cmd {
	return func() tea.Msg {
		// Real source deleted first, local cache row only removed once that
		// actually succeeds — used to delete the cache row unconditionally
		// and discard the real delete's error, which on a failure (stale id,
		// file already gone) left the real note/file alive and untracked
		// while the cache said it was gone. Same bug class fixed in
		// calctl's DeleteEvent on 2026-08-01.
		// The obsidian/markdown default only fires when relPath is actually
		// set — syncdispatch.DeleteBySource's default calls notes.Delete
		// unconditionally, and an empty relPath there would delete
		// vaultPath itself.
		if src := config.Source(); src == config.SourceApple || src == config.SourceJoplin || relPath != "" {
			if err := syncdispatch.DeleteBySource(src, id, config.VaultPath(), relPath); err != nil {
				return deletedMsg{err}
			}
		}
		s, err := store.New(config.DBPath(), config.Shared())
		if err != nil {
			return deletedMsg{err}
		}
		defer s.Close()
		if err := s.Delete(context.Background(), id); err != nil {
			return deletedMsg{err}
		}
		actlog.Deleted(title)
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
		switch config.Source() {
		case config.SourceApple:
			id, werr := notes.WriteApple("", n.Title, notes.TextToHTML(n.Body), n.Folder)
			if werr != nil {
				return noteRestoredMsg{err: werr}
			}
			restored = &models.Note{
				ID: id, Title: n.Title, Body: n.Body,
				Folder: n.Folder, Source: "apple",
				ModTime: time.Now(), Created: time.Now(),
			}
		case config.SourceJoplin:
			id, werr := notes.WriteJoplin("", n.Title, n.Body, n.Folder)
			if werr != nil {
				return noteRestoredMsg{err: werr}
			}
			restored = &models.Note{
				ID: id, Title: n.Title, Body: n.Body,
				Folder: n.Folder, Source: "joplin",
				ModTime: time.Now(), Created: time.Now(),
			}
		default:
			restored, err = notes.Write(config.VaultPath(), n.Title, n.Body, n.Tags, n.Folder, n.EventID)
			if err != nil {
				return noteRestoredMsg{err: err}
			}
		}
		s, err := store.New(config.DBPath(), config.Shared())
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

		var appleBody string
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
			appleBody = notes.ReconcileBlocks(editBlocks, plainBody)
		}
		n, err := syncdispatch.WriteBySource(config.Source(), syncdispatch.WriteParams{
			ID: id, Title: title, Body: body, AppleBody: appleBody,
			Tags: tags, Folder: folder, VaultPath: config.VaultPath(),
		})
		if err != nil {
			return writeDoneMsg{err: err}
		}

		if s, serr := store.New(config.DBPath(), config.Shared()); serr == nil {
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
