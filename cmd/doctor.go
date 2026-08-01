package cmd

import (
	"os"

	"github.com/aeon022/missionctl-core/doctor"
	"github.com/aeon022/notectl/internal/config"
	"github.com/spf13/cobra"
)

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Check config, database, and (if configured) Notes.app health",
	Run: func(cmd *cobra.Command, args []string) {
		checks := []doctor.Check{
			doctor.CheckSQLite("Database", config.DBPath(), "notes"),
		}
		if config.Source() == config.SourceApple {
			checks = append(checks, doctor.CheckAppleApp("Notes.app", "Notes"))
		}
		if !doctor.PrintReport(checks) {
			os.Exit(1)
		}
	},
}

func init() {
	rootCmd.AddCommand(doctorCmd)
}
