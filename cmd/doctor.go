package cmd

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/aeon022/missionctl-core/doctor"
	"github.com/aeon022/notectl/internal/config"
	"github.com/spf13/cobra"
)

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Check config, database, and (if configured) Notes.app/Joplin health",
	Run: func(cmd *cobra.Command, args []string) {
		checks := []doctor.Check{
			doctor.CheckSQLite("Database", config.DBPath(), "notes"),
			doctor.CheckDataDir("Data directory", config.DBPath(), config.Shared()),
		}
		switch config.Source() {
		case config.SourceApple:
			checks = append(checks, doctor.CheckAppleApp("Notes.app", "Notes"))
		case config.SourceJoplin:
			checks = append(checks, checkJoplin())
		default:
			checks = append(checks, checkVaultPath())
		}
		if !doctor.PrintReport(checks) {
			os.Exit(1)
		}
	},
}

// checkJoplin verifies Joplin's Data API is reachable (GET /ping needs no
// token) and that a token is actually configured — both are required for
// every other Joplin operation, so surfacing either gap here beats a
// confusing failure mid-sync.
func checkJoplin() doctor.Check {
	apiURL := config.JoplinAPIURL()
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(apiURL + "/ping")
	if err != nil {
		return doctor.Check{
			Label: "Joplin Data API", OK: false,
			Detail: fmt.Sprintf("not reachable at %s — start Joplin and enable Options → Web Clipper: %v", apiURL, err),
		}
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return doctor.Check{Label: "Joplin Data API", OK: false, Detail: fmt.Sprintf("unexpected response from %s: %d %s", apiURL, resp.StatusCode, string(body))}
	}

	if config.JoplinToken() == "" {
		return doctor.Check{
			Label: "Joplin Data API", OK: false,
			Detail: "reachable, but no token configured — set joplin_token in ~/.config/notectl/notectl.yaml or NOTECTL_JOPLIN_TOKEN (Options → Web Clipper)",
		}
	}
	return doctor.Check{Label: "Joplin Data API", OK: true, Detail: apiURL}
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
