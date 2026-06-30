package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/aeon022/notectl/internal/config"
	"github.com/aeon022/notectl/internal/notes"
	"github.com/aeon022/notectl/internal/store"
	"github.com/spf13/cobra"
)

var searchCmd = &cobra.Command{
	Use:   "search <query>",
	Short: "Search notes by title and content",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		query := args[0]
		asJSON, _ := cmd.Flags().GetBool("json")
		limit, _ := cmd.Flags().GetInt("limit")

		// try SQLite first (fast FTS)
		s, err := store.New(config.DBPath())
		if err != nil {
			return err
		}
		defer s.Close()

		results, err := s.List(context.Background(), store.Filter{Query: query, Limit: limit})
		if err != nil {
			return err
		}

		// fall back to live file search if cache is empty
		if len(results) == 0 {
			results, err = notes.Search(config.VaultPath(), query, limit)
			if err != nil {
				return err
			}
		}

		if asJSON {
			return json.NewEncoder(os.Stdout).Encode(results)
		}
		if len(results) == 0 {
			fmt.Println("No notes found.")
			return nil
		}
		for _, n := range results {
			fmt.Printf("%-30s  %s  %s\n",
				n.Title,
				n.ModTime.Format("Jan 02 2006"),
				n.Folder)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(searchCmd)
	searchCmd.Flags().Bool("json", false, "Output as JSON")
	searchCmd.Flags().IntP("limit", "n", 20, "Max results")
}
