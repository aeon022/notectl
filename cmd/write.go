package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/aeon022/notectl/internal/config"
	"github.com/aeon022/notectl/internal/store"
	"github.com/aeon022/notectl/internal/syncdispatch"
	"github.com/spf13/cobra"
)

var writeCmd = &cobra.Command{
	Use:   "write <title>",
	Short: "Write a note to the configured source — Apple Notes or the Obsidian vault (body from --body or stdin)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		title := args[0]
		body, _ := cmd.Flags().GetString("body")
		folder, _ := cmd.Flags().GetString("folder")
		tagsStr, _ := cmd.Flags().GetString("tags")
		eventID, _ := cmd.Flags().GetString("event-id")

		// read body from stdin if not provided
		if body == "" {
			data, err := io.ReadAll(os.Stdin)
			if err != nil {
				return err
			}
			body = string(data)
		}

		var tags []string
		if tagsStr != "" {
			for _, t := range strings.Split(tagsStr, ",") {
				if t = strings.TrimSpace(t); t != "" {
					tags = append(tags, t)
				}
			}
		}

		n, werr := syncdispatch.WriteBySource(config.Source(), syncdispatch.WriteParams{
			Title: title, Body: body, Tags: tags, EventID: eventID, Folder: folder, VaultPath: config.VaultPath(),
		})
		if werr != nil {
			return werr
		}

		// update SQLite cache
		s, serr := store.New(config.DBPath(), config.Shared())
		if serr == nil {
			defer s.Close()
			_ = s.Upsert(context.Background(), n)
		}

		fmt.Printf("Wrote: %s (%s)\n", n.Title, n.Path)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(writeCmd)
	writeCmd.Flags().StringP("body", "b", "", "Note body (default: read from stdin)")
	writeCmd.Flags().StringP("folder", "f", "", "Subfolder within vault")
	writeCmd.Flags().StringP("tags", "t", "", "Comma-separated tags")
	writeCmd.Flags().String("event-id", "", "Link to a calendar event by ID (e.g. from calctl create_event)")
}
