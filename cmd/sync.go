package cmd

import (
	"context"
	"fmt"

	"github.com/aeon022/notectl/internal/config"
	"github.com/aeon022/notectl/internal/notes"
	"github.com/aeon022/notectl/internal/store"
	"github.com/spf13/cobra"
)

var syncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Sync notes from configured source into local cache",
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := store.New(config.DBPath())
		if err != nil {
			return err
		}
		defer s.Close()
		ctx := context.Background()

		switch config.Source() {
		case config.SourceApple:
			folder := config.AppleFolder()
			fmt.Print("Syncing Apple Notes")
			if folder != "" {
				fmt.Printf(" (folder: %s)", folder)
			}
			fmt.Println()
			ns, aerr := notes.ListApple(folder)
			if aerr != nil {
				return fmt.Errorf("apple notes: %w", aerr)
			}
			_ = s.DeleteBySource(ctx, "apple")
			for i := range ns {
				_ = s.Upsert(ctx, &ns[i])
			}
			fmt.Printf("  %d notes indexed\n", len(ns))

		default:
			vault := config.VaultPath()
			fmt.Printf("Syncing vault: %s\n", vault)
			ns, verr := notes.List(vault)
			if verr != nil {
				return fmt.Errorf("vault scan: %w", verr)
			}
			_ = s.DeleteBySource(ctx, "obsidian")
			for i := range ns {
				_ = s.Upsert(ctx, &ns[i])
			}
			fmt.Printf("  %d notes indexed\n", len(ns))
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(syncCmd)
}
