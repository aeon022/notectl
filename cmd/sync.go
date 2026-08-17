package cmd

import (
	"context"
	"fmt"

	"github.com/aeon022/notectl/internal/config"
	"github.com/aeon022/notectl/internal/mirror"
	"github.com/aeon022/notectl/internal/store"
	"github.com/aeon022/notectl/internal/syncdispatch"
	"github.com/spf13/cobra"
)

var applyMirrorDeletes bool

var syncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Sync notes from the configured source(s) into local cache",
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := store.New(config.DBPath(), config.Shared())
		if err != nil {
			return err
		}
		defer s.Close()
		ctx := context.Background()

		params := syncdispatch.ParamsFromConfig()
		var lastErr error
		for _, src := range config.SyncSources() {
			fmt.Print(syncLabel(src, params))
			ns, err := syncdispatch.List(src, params)
			if err != nil {
				fmt.Printf(" — failed: %v\n", err)
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
			fmt.Printf("\n  %d notes indexed\n", len(ns))
		}

		if config.MirrorEnabled() {
			if !hasBothMirrorSources(config.SyncSources()) {
				fmt.Println("\nmirror_apple_obsidian is set, but sync_sources doesn't include both apple and obsidian — skipping mirror sync.")
			} else {
				report, mErr := mirror.Sync(ctx, s, params.VaultPath)
				fmt.Printf("\nMirror sync: %d created, %d updated, %d already linked, %d pending delete(s)\n",
					report.Created, report.Updated, report.LinkedExisting, report.PendingDeletes)
				for _, e := range report.Errors {
					fmt.Println("  ! " + e)
				}
				if report.PendingDeletes > 0 {
					fmt.Println("  Run 'notectl sync --apply-deletes' to remove the mirrored copies.")
				}
				if mErr != nil {
					lastErr = mErr
				}
			}
		}

		if applyMirrorDeletes {
			applied, errs, aErr := mirror.ApplyPendingDeletes(ctx, s, config.VaultPath())
			fmt.Printf("\nApplied %d pending mirror deletion(s)\n", applied)
			for _, e := range errs {
				fmt.Println("  ! " + e)
			}
			if aErr != nil {
				lastErr = aErr
			}
		}

		return lastErr
	},
}

func syncLabel(src config.SourceType, p syncdispatch.Params) string {
	switch src {
	case config.SourceApple:
		if p.AppleFolder != "" {
			return fmt.Sprintf("Syncing Apple Notes (folder: %s)", p.AppleFolder)
		}
		return "Syncing Apple Notes"
	case config.SourceJoplin:
		if p.JoplinFolder != "" {
			return fmt.Sprintf("Syncing Joplin (notebook: %s)", p.JoplinFolder)
		}
		return "Syncing Joplin"
	default:
		return fmt.Sprintf("Syncing vault: %s", p.VaultPath)
	}
}

// hasBothMirrorSources reports whether sources covers both an Apple Notes
// source and an obsidian-backed source (obsidian or markdown — they share
// one backend and both tag their cache rows "obsidian", see
// syncdispatch.SourceKey) — the minimum mirror.Sync needs to have anything
// to diff against on both sides.
func hasBothMirrorSources(sources []config.SourceType) bool {
	var hasApple, hasObsidian bool
	for _, s := range sources {
		switch s {
		case config.SourceApple:
			hasApple = true
		case config.SourceObsidian, config.SourceMarkdown:
			hasObsidian = true
		}
	}
	return hasApple && hasObsidian
}

func init() {
	syncCmd.Flags().BoolVar(&applyMirrorDeletes, "apply-deletes", false, "Apply queued mirror deletions (see mirror_apple_obsidian)")
	rootCmd.AddCommand(syncCmd)
}
