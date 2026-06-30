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
	Short: "Sync Obsidian vault (and Apple Notes if configured) into local cache",
	RunE: func(cmd *cobra.Command, args []string) error {
		apple, _ := cmd.Flags().GetBool("apple")

		s, err := store.New(config.DBPath())
		if err != nil {
			return err
		}
		defer s.Close()
		ctx := context.Background()

		// ── Obsidian sync ──
		vault := config.VaultPath()
		fmt.Printf("Syncing Obsidian vault: %s\n", vault)
		obsNotes, err := notes.List(vault)
		if err != nil {
			return fmt.Errorf("vault scan: %w", err)
		}
		_ = s.DeleteBySource(ctx, "obsidian")
		for i := range obsNotes {
			if e := s.Upsert(ctx, &obsNotes[i]); e != nil {
				fmt.Fprintf(nil, "warn: %s: %v\n", obsNotes[i].Title, e)
			}
		}
		fmt.Printf("  %d notes indexed\n", len(obsNotes))

		// ── Apple Notes sync (optional) ──
		if apple {
			folder := config.AppleFolder()
			fmt.Print("Syncing Apple Notes")
			if folder != "" {
				fmt.Printf(" (folder: %s)", folder)
			}
			fmt.Println()
			appleNotes, aerr := notes.ListApple(folder)
			if aerr != nil {
				return fmt.Errorf("apple notes: %w", aerr)
			}
			_ = s.DeleteBySource(ctx, "apple")
			for i := range appleNotes {
				_ = s.Upsert(ctx, &appleNotes[i])
			}
			fmt.Printf("  %d notes indexed\n", len(appleNotes))
		}

		return nil
	},
}

func init() {
	rootCmd.AddCommand(syncCmd)
	syncCmd.Flags().Bool("apple", false, "Also sync Apple Notes")
}
