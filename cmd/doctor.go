package cmd

import (
	"fmt"
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
			doctor.CheckDataDir("Data directory", config.DBPath(), config.Shared()),
		}
		if config.Source() == config.SourceApple {
			checks = append(checks, doctor.CheckAppleApp("Notes.app", "Notes"))
		} else {
			checks = append(checks, checkVaultPath())
		}
		if !doctor.PrintReport(checks) {
			os.Exit(1)
		}
	},
}

// checkVaultPath warns when vault_path is still the built-in default
// (~/Documents/Notes) rather than a real, deliberately-chosen vault — every
// `write`/write_note call for source: obsidian/markdown lands there, so a
// vault_path nobody actually set is the same silent-wrong-location trap
// source: apple notes hit before write_note learned to branch on Source().
func checkVaultPath() doctor.Check {
	raw := config.VaultPathRaw()
	if raw != "~/Documents/Notes" {
		return doctor.Check{Label: "Vault path", OK: true, Detail: config.VaultPath()}
	}
	return doctor.Check{
		Label: "Vault path",
		OK:    true,
		Detail: fmt.Sprintf(
			"%s (default — still unset if this isn't really your vault; set vault_path in ~/.config/notectl/notectl.yaml or NOTECTL_VAULT_PATH)",
			config.VaultPath()),
	}
}

func init() {
	rootCmd.AddCommand(doctorCmd)
}
