package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/aeon022/notectl/internal/config"
	"github.com/aeon022/notectl/internal/notes"
	"github.com/aeon022/notectl/internal/store"
	"github.com/spf13/cobra"
)

var writeCmd = &cobra.Command{
	Use:   "write <title>",
	Short: "Write a note to the Obsidian vault (body from --body or stdin)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		title := args[0]
		body, _ := cmd.Flags().GetString("body")
		folder, _ := cmd.Flags().GetString("folder")
		tagsStr, _ := cmd.Flags().GetString("tags")

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

		n, err := notes.Write(config.VaultPath(), title, body, tags, folder)
		if err != nil {
			return err
		}

		// update SQLite cache
		s, serr := store.New(config.DBPath())
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
}
