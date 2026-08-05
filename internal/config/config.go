package config

import (
	"fmt"
	"os"
	"path/filepath"

	coreconfig "github.com/aeon022/missionctl-core/config"
	"github.com/aeon022/missionctl-core/licensing"
	"github.com/spf13/viper"
)

// SourceType identifies the notes backend.
type SourceType string

const (
	SourceObsidian SourceType = "obsidian"
	SourceJoplin   SourceType = "joplin"
	SourceMarkdown SourceType = "markdown"
	SourceApple    SourceType = "apple"
)

func Init() {
	viper.SetConfigName("notectl")
	viper.SetConfigType("yaml")
	viper.AddConfigPath(configDir())
	viper.AddConfigPath(".")
	viper.SetEnvPrefix("NOTECTL")
	viper.AutomaticEnv()
	_ = viper.ReadInConfig()
}

// Save writes the current viper config to ~/.config/notectl/notectl.yaml.
func Save(vaultPath string, source SourceType) error {
	dir := configDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	viper.Set("vault_path", contractHome(vaultPath))
	viper.Set("source", string(source))
	cfgFile := filepath.Join(dir, "notectl.yaml")
	if err := viper.WriteConfigAs(cfgFile); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}

// Source returns the configured source type (default: obsidian).
func Source() SourceType {
	s := SourceType(viper.GetString("source"))
	switch s {
	case SourceApple, SourceJoplin, SourceMarkdown:
		return s
	default:
		return SourceObsidian
	}
}

// VaultPathRaw returns the vault path as stored in config (may contain ~).
func VaultPathRaw() string {
	if p := viper.GetString("vault_path"); p != "" {
		return p
	}
	return "~/Documents/Notes"
}

func VaultPath() string {
	return expandHome(VaultPathRaw())
}

// DBPath returns the database file path: data_dir (viper key, also settable
// via NOTECTL_DATA_DIR) takes precedence — points it at a user-chosen
// directory, e.g. inside iCloud Drive or Dropbox, resolved via
// coreconfig.ResolveDir — then the legacy db_path (a full file path), then
// the private default (~/.local/share/notectl).
func DBPath() string {
	if dir := viper.GetString("data_dir"); dir != "" {
		resolved, _ := coreconfig.ResolveDir("notectl", dir)
		return filepath.Join(resolved, "notes.db")
	}
	if p := viper.GetString("db_path"); p != "" {
		return expandHome(p)
	}
	dir, _ := coreconfig.ResolveDir("notectl", "")
	return filepath.Join(dir, "notes.db")
}

// Shared reports whether DBPath currently resolves to a user-configured
// directory (data_dir) rather than the tool's private default.
func Shared() bool {
	return viper.GetString("data_dir") != ""
}

// LastSyncedPath is the marker file (see missionctl-core/lastsync) tracking
// when a sync last completed, for the TUI's "synced Xh ago" indicator.
func LastSyncedPath() string {
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".local", "share", "notectl")
	_ = os.MkdirAll(dir, 0o755)
	return filepath.Join(dir, "last_synced")
}

// UIStatePath is where the TUI persists small preferences (last active
// folder tab) — see missionctl-core/uistate.
func UIStatePath() string {
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".local", "share", "notectl")
	_ = os.MkdirAll(dir, 0o755)
	return filepath.Join(dir, "ui_state.json")
}

func AppleFolder() string {
	return viper.GetString("apple_folder") // optional: Apple Notes folder to sync
}

func configDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "notectl")
}

// bundleBenefitID and notectlBenefitID identify the missionctl Bundle's and
// notectl's own individual-product license-key benefits in Polar. Both
// start empty (the notectl-only product doesn't exist in Polar yet) — see
// licensing.Result.Grants: empty IDs fall back to "any active key under
// our org grants access", so this is a no-op until both are filled in
// once the individual product is created and its benefit ID is known.
const (
	bundleBenefitID  = ""
	notectlBenefitID = ""
)

// IsPro reports whether a valid Pro/Bundle or notectl-only license is
// active on this machine — gates having more than one named vault.
func IsPro() bool {
	result := licensing.Result{Status: LicenseStatus(), BenefitID: LicenseBenefitID()}
	return result.Grants(notectlBenefitID, bundleBenefitID)
}

func LicenseKey() string {
	return viper.GetString("license_key")
}

func LicenseStatus() string {
	return viper.GetString("license_status")
}

func LicenseBenefitID() string {
	return viper.GetString("license_benefit_id")
}

func PolarOrgID() string {
	if v := viper.GetString("polar_org_id"); v != "" {
		return v
	}
	return licensing.DefaultOrgID
}

// SetLicense persists the license key/status/benefit to
// ~/.config/notectl/notectl.yaml.
func SetLicense(key, status, benefitID string) error {
	viper.Set("license_key", key)
	viper.Set("license_status", status)
	viper.Set("license_benefit_id", benefitID)
	dir := configDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return viper.WriteConfigAs(filepath.Join(dir, "notectl.yaml"))
}

// Vaults returns the configured named vaults (name -> path). Empty if the
// user has never used `notectl vault add`.
func Vaults() map[string]string {
	raw := viper.GetStringMapString("vaults")
	out := make(map[string]string, len(raw))
	for name, path := range raw {
		out[name] = path
	}
	return out
}

// VaultAdd registers a named vault. Adding a second named vault requires an
// active Pro/Bundle license — the free tier is limited to one vault (via
// vault_path, or a single named vault).
func VaultAdd(name, path string) error {
	vaults := Vaults()
	if _, exists := vaults[name]; !exists && len(vaults) >= 1 && !IsPro() {
		return fmt.Errorf("multiple vaults require the missionctl Bundle — get it at https://missionctl.sh/#pricing, then: notectl license activate <key>")
	}
	vaults[name] = contractHome(path)
	viper.Set("vaults", vaults)
	dir := configDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return viper.WriteConfigAs(filepath.Join(dir, "notectl.yaml"))
}

// VaultUse switches the active vault to a previously-added named vault by
// pointing vault_path at it.
func VaultUse(name string) error {
	vaults := Vaults()
	path, ok := vaults[name]
	if !ok {
		return fmt.Errorf("no vault named %q — see: notectl vault list", name)
	}
	return Save(expandHome(path), Source())
}

func expandHome(p string) string {
	if len(p) >= 2 && p[:2] == "~/" {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, p[2:])
	}
	return p
}

func contractHome(p string) string {
	home, _ := os.UserHomeDir()
	if len(p) > len(home) && p[:len(home)] == home {
		return "~" + p[len(home):]
	}
	return p
}
