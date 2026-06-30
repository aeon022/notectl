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

var readCmd = &cobra.Command{
	Use:   "read <title>",
	Short: "Read a note by title",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		title := args[0]
		asJSON, _ := cmd.Flags().GetBool("json")

		// try SQLite cache first
		s, err := store.New(config.DBPath())
		if err != nil {
			return err
		}
		defer s.Close()

		n, err := s.GetByTitle(context.Background(), title)
		if err != nil {
			return err
		}

		// fall back to live read from vault
		if n == nil {
			n, err = notes.Read(config.VaultPath(), title)
			if err != nil {
				return err
			}
		}
		if n == nil {
			return fmt.Errorf("note %q not found", title)
		}

		if asJSON {
			return json.NewEncoder(os.Stdout).Encode(n)
		}
		fmt.Printf("# %s\n", n.Title)
		if n.Folder != "" {
			fmt.Printf("Folder: %s\n", n.Folder)
		}
		if len(n.Tags) > 0 {
			fmt.Printf("Tags: %s\n", joinTags(n.Tags))
		}
		fmt.Printf("Modified: %s\n\n", n.ModTime.Format("Mon, 02 Jan 2006 15:04"))
		fmt.Println(n.Body)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(readCmd)
	readCmd.Flags().Bool("json", false, "Output as JSON")
}
