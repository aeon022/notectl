package cmd

import (
	"github.com/aeon022/notectl/internal/tui"
	"github.com/spf13/cobra"
)

var flagOpenPath string

var tuiCmd = &cobra.Command{
	Use:   "tui",
	Short: "Open interactive notes browser (default)",
	RunE: func(cmd *cobra.Command, args []string) error {
		return tui.Run(flagOpenPath)
	},
}

func init() {
	rootCmd.AddCommand(tuiCmd)
	rootCmd.RunE = tuiCmd.RunE
	rootCmd.Flags().StringVar(&flagOpenPath, "open", "", "Open directly on this note's detail view (by vault-relative path) — for jumping in from another tool's linked entry")
}
