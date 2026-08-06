package cmd

import (
	"context"
	"fmt"

	"github.com/aeon022/notectl/internal/config"
	"github.com/aeon022/notectl/internal/store"
	"github.com/aeon022/notectl/internal/syncdispatch"
	"github.com/spf13/cobra"
)

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
			fmt.Printf("\n  %d notes indexed\n", len(ns))
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
	rootCmd.AddCommand(syncCmd)
}
