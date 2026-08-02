package cmd

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"

	"github.com/aeon022/notectl/internal/config"
	"github.com/aeon022/notectl/internal/store"
	"github.com/spf13/cobra"
)

var (
	exportFolder string
	exportFormat string
	exportOutput string
)

var exportCmd = &cobra.Command{
	Use:   "export",
	Short: "Export notes to CSV or JSON",
	Long: `Export notes (optionally scoped to one folder) as CSV or JSON —
same --format/--output pattern budgetctl's and diaryctl's own "export"
commands use.

Examples:
  notectl export --format json
  notectl export --folder Recipes --format csv -o recipes.csv`,
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := store.New(config.DBPath(), config.Shared())
		if err != nil {
			return err
		}
		defer s.Close()

		notes, err := s.List(context.Background(), store.Filter{Folder: exportFolder, Limit: 100000})
		if err != nil {
			return fmt.Errorf("listing notes: %w", err)
		}

		w := os.Stdout
		if exportOutput != "" {
			f, err := os.Create(exportOutput)
			if err != nil {
				return fmt.Errorf("create output file: %w", err)
			}
			defer f.Close()
			w = f
		}

		switch exportFormat {
		case "json":
			enc := json.NewEncoder(w)
			enc.SetIndent("", "  ")
			return enc.Encode(notes)
		default: // csv
			cw := csv.NewWriter(w)
			_ = cw.Write([]string{"title", "folder", "tags", "source", "mod_time", "body"})
			for _, n := range notes {
				_ = cw.Write([]string{n.Title, n.Folder, fmt.Sprint(n.Tags), n.Source, n.ModTime.Format("2006-01-02T15:04:05"), n.Body})
			}
			cw.Flush()
			if exportOutput != "" {
				fmt.Fprintf(os.Stderr, "Exported %d note(s) → %s\n", len(notes), exportOutput)
			}
			return cw.Error()
		}
	},
}

func init() {
	exportCmd.Flags().StringVar(&exportFolder, "folder", "", "Only export notes in this folder (default: all)")
	exportCmd.Flags().StringVar(&exportFormat, "format", "csv", "Output format: csv | json")
	exportCmd.Flags().StringVarP(&exportOutput, "output", "o", "", "Output file (default: stdout)")
	rootCmd.AddCommand(exportCmd)
}
