package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/aeon022/notectl/internal/config"
	"github.com/aeon022/notectl/internal/notes"
	"github.com/aeon022/notectl/internal/store"
	"github.com/spf13/cobra"
)

var dailyCmd = &cobra.Command{
	Use:   "daily",
	Short: "Open or create today's daily note",
	RunE: func(cmd *cobra.Command, args []string) error {
		folder, _ := cmd.Flags().GetString("folder")
		open, _ := cmd.Flags().GetBool("open")

		today := time.Now().Format("2006-01-02")
		vaultPath := config.VaultPath()

		// check if note already exists
		existing, _ := notes.Read(vaultPath, today)
		if existing != nil {
			fmt.Println(existing.Body)
			if open {
				return openInEditor(existing.Path)
			}
			return nil
		}

		// create with template
		body := dailyTemplate(time.Now())
		n, err := notes.Write(vaultPath, today, body, []string{"daily"}, folder)
		if err != nil {
			return fmt.Errorf("create daily note: %w", err)
		}

		// update SQLite cache
		if s, serr := store.New(config.DBPath()); serr == nil {
			defer s.Close()
			_ = s.Upsert(context.Background(), n)
		}

		fmt.Printf("Created: %s\n", n.Path)
		if open {
			return openInEditor(n.Path)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(dailyCmd)
	dailyCmd.Flags().StringP("folder", "f", "Daily", "Subfolder for daily notes")
	dailyCmd.Flags().BoolP("open", "o", false, "Open in $EDITOR after creating")
}

func dailyTemplate(t time.Time) string {
	return fmt.Sprintf(`## Focus


## Tasks
- [ ]

## Notes


## Log

`)
}

func openInEditor(relPath string) error {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "nano"
	}
	full := relPath
	if !isAbs(relPath) {
		full = config.VaultPath() + "/" + relPath
	}
	return exec.Command(editor, full).Run()
}

func isAbs(p string) bool {
	return len(p) > 0 && p[0] == '/'
}
