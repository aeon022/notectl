package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/aeon022/notectl/internal/config"
	"github.com/aeon022/notectl/internal/store"
	"github.com/spf13/cobra"
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List notes from the local cache",
	RunE: func(cmd *cobra.Command, args []string) error {
		asJSON, _ := cmd.Flags().GetBool("json")
		query, _ := cmd.Flags().GetString("query")
		folder, _ := cmd.Flags().GetString("folder")
		source, _ := cmd.Flags().GetString("source")
		limit, _ := cmd.Flags().GetInt("limit")

		s, err := store.New(config.DBPath())
		if err != nil {
			return err
		}
		defer s.Close()

		notes, err := s.List(context.Background(), store.Filter{
			Source: source,
			Folder: folder,
			Query:  query,
			Limit:  limit,
		})
		if err != nil {
			return err
		}

		if asJSON {
			return json.NewEncoder(os.Stdout).Encode(notes)
		}
		for _, n := range notes {
			tag := ""
			if len(n.Tags) > 0 {
				tag = " [" + joinTags(n.Tags) + "]"
			}
			folder := ""
			if n.Folder != "" {
				folder = " (" + n.Folder + ")"
			}
			fmt.Printf("%s  %s%s%s\n",
				n.ModTime.Format("Jan 02 15:04"),
				n.Title, folder, tag)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(listCmd)
	listCmd.Flags().Bool("json", false, "Output as JSON")
	listCmd.Flags().StringP("query", "q", "", "Search query")
	listCmd.Flags().StringP("folder", "f", "", "Filter by folder")
	listCmd.Flags().StringP("source", "s", "", "Filter by source (obsidian|apple)")
	listCmd.Flags().IntP("limit", "n", 100, "Max results")
}

func joinTags(tags []string) string {
	out := ""
	for i, t := range tags {
		if i > 0 {
			out += ", "
		}
		out += t
	}
	return out
}
