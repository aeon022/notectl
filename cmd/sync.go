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

		// Mutually exclusive on purpose: --apply-deletes must only ever act
		// on deletions a PRIOR sync queued and the user has seen, so it
		// never runs a mirror pass that could queue and apply a deletion in
		// the same invocation.
		switch {
		case applyMirrorDeletes:
			applied, errs, aErr := mirror.ApplyPendingDeletes(ctx, s, params.VaultPath, params.ExcludeFolders)
			fmt.Printf("\nApplied %d pending mirror deletion(s)\n", applied)
			for _, e := range errs {
				fmt.Println("  ! " + e)
			}
			if aErr != nil {
				lastErr = aErr
			}
		case config.MirrorEnabled():
			if !config.MirrorSourcesConfigured() {
				fmt.Println("\nmirror_apple_obsidian is set, but sync_sources doesn't include both apple and obsidian — skipping mirror sync.")
				break
			}
			report, mErr := mirror.Sync(ctx, s, params.VaultPath, params.AppleFolder, params.ExcludeFolders)
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

func init() {
	syncCmd.Flags().BoolVar(&applyMirrorDeletes, "apply-deletes", false, "Apply queued mirror deletions (see mirror_apple_obsidian)")
	rootCmd.AddCommand(syncCmd)
}
