package cmd

import (
	"github.com/aeon022/notectl/internal/tui"
	"github.com/spf13/cobra"
)

var tuiCmd = &cobra.Command{
	Use:   "tui",
	Short: "Open interactive notes browser (default)",
	RunE: func(cmd *cobra.Command, args []string) error {
		return tui.Run()
	},
}

func init() {
	rootCmd.AddCommand(tuiCmd)
	rootCmd.RunE = tuiCmd.RunE
}
