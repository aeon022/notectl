package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	coreconfig "github.com/aeon022/missionctl-core/config"
	"github.com/aeon022/missionctl-core/licensing"
	"gopkg.in/yaml.v3"
)

// SourceType identifies the notes backend.
type SourceType string

const (
	SourceObsidian SourceType = "obsidian"
	SourceJoplin   SourceType = "joplin"
	SourceMarkdown SourceType = "markdown"
	SourceApple    SourceType = "apple"
)

// The ~20 flat config keys live in a plain map[string]interface{} loaded
// from YAML, rather than a typed struct — Get/Set below need to address any
// key by name (mirroring the old viper.Get*/Set generic API), and callers
// each apply their own per-key default/validation logic.
//
// Precedence (highest to lowest), matching the old viper setup exactly:
// explicit Set() (config.Save/SetLicense/VaultAdd, or a test override) >
// NOTECTL_* env var > notectl.yaml > per-key default in each getter below.
var (
	mu        sync.RWMutex
	fileData  = map[string]interface{}{}
	overrides = map[string]interface{}{}
	// envEnabled mirrors viper.AutomaticEnv() only taking effect once Init()
	// calls it: production code paths (cmd/root.go) call Init() and so pick
	// up NOTECTL_* env vars, but tests that never call Init() must not have
	// a developer's real env (e.g. NOTECTL_DATA_DIR from their shell
	// profile) leak into what's supposed to be an isolated test config.
	envEnabled bool
)

// Init loads ~/.config/notectl/notectl.yaml, falling back to ./notectl.yaml
// (same search order as the old viper.AddConfigPath calls: configDir()
// first, then the working directory), and enables NOTECTL_* env var
// overrides. Missing/unreadable file is not an error — same as the old
// `_ = viper.ReadInConfig()`.
func Init() {
	mu.Lock()
	defer mu.Unlock()
	envEnabled = true
	fileData = map[string]interface{}{}
	for _, dir := range []string{configDir(), "."} {
		b, err := os.ReadFile(filepath.Join(dir, "notectl.yaml"))
		if err != nil {
			continue
		}
		_ = yaml.Unmarshal(b, &fileData)
		return
	}
}

// set records an explicit in-memory override — the old viper.Set.
func set(key string, val interface{}) {
	mu.Lock()
	defer mu.Unlock()
	overrides[key] = val
}

// SetForTest overrides a config key in-memory for the duration of a test —
// the highest-precedence source, same as the old direct viper.Set(...)
// calls test code used before this package moved off viper. Not for
// production use.
func SetForTest(key, value string) { set(key, value) }

// GetForTest reads a config key's current effective string value (override,
// then env, then file) — for tests that snapshot/restore a value around
// SetForTest.
func GetForTest(key string) string { return getString(key) }

// lookup resolves a key through the precedence chain — override, then env
// (once Init has enabled it), then file — and reports whether anything set
// it at all.
func lookup(key string) (interface{}, bool) {
	mu.RLock()
	defer mu.RUnlock()
	if v, ok := overrides[key]; ok {
		return v, true
	}
	if envEnabled {
		if v, ok := os.LookupEnv("NOTECTL_" + strings.ToUpper(key)); ok {
			return v, true
		}
	}
	if v, ok := fileData[key]; ok {
		return v, true
	}
	return nil, false
}

func getString(key string) string {
	v, ok := lookup(key)
	if !ok {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}

func getBool(key string) bool {
	v, ok := lookup(key)
	if !ok {
		return false
	}
	if b, ok := v.(bool); ok {
		return b
	}
	s := fmt.Sprint(v)
	return s == "true" || s == "1"
}

func getStringMapString(key string) map[string]string {
	mu.RLock()
	defer mu.RUnlock()
	if v, ok := overrides[key]; ok {
		if m, ok := v.(map[string]string); ok {
			return m
		}
	}
	if v, ok := fileData[key]; ok {
		return toStringMapString(v)
	}
	return nil
}

// toStringMapString converts a nested YAML mapping (yaml.v3 decodes it into
// map[string]interface{}) into map[string]string, same effective result as
// viper.GetStringMapString.
func toStringMapString(v interface{}) map[string]string {
	m, ok := v.(map[string]interface{})
	if !ok {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, val := range m {
		out[k] = fmt.Sprint(val)
	}
	return out
}

// writeConfig persists the merged (file + override) config to path, then
// folds overrides into fileData — same net effect as the old
// viper.WriteConfigAs, which wrote viper's whole current merged config.
func writeConfig(path string) error {
	mu.Lock()
	defer mu.Unlock()
	merged := make(map[string]interface{}, len(fileData)+len(overrides))
	for k, v := range fileData {
		merged[k] = v
	}
	for k, v := range overrides {
		merged[k] = v
	}
	b, err := yaml.Marshal(merged)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return err
	}
	fileData = merged
	overrides = map[string]interface{}{}
	return nil
}

// Save writes the current config to ~/.config/notectl/notectl.yaml.
func Save(vaultPath string, source SourceType) error {
	dir := configDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	set("vault_path", contractHome(vaultPath))
	set("source", string(source))
	cfgFile := filepath.Join(dir, "notectl.yaml")
	if err := writeConfig(cfgFile); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}

// Source returns the configured source type (default: obsidian). It also
// remains the single write target — creating a note always needs one
// unambiguous destination, so this isn't affected by SyncSources below.
func Source() SourceType {
	s := SourceType(getString("source"))
	switch s {
	case SourceApple, SourceJoplin, SourceMarkdown:
		return s
	default:
		return SourceObsidian
	}
}

// SyncSources returns every source that `sync` should pull from — set via
// sync_sources: a comma-separated list in config (e.g. "apple,joplin") or
// NOTECTL_SYNC_SOURCES. Falls back to just [Source()] when unset, so
// existing single-source setups are unaffected. The local cache already
// tags rows by source and scopes deletes per-source (DeleteBySource), so
// multiple sources coexisting there was already safe — this just makes
// "sync all of them in one go" configurable instead of requiring a
// temporary NOTECTL_SOURCE override per source.
func SyncSources() []SourceType {
	raw := getString("sync_sources")
	if raw == "" {
		return []SourceType{Source()}
	}
	var out []SourceType
	seen := make(map[SourceType]bool)
	for _, part := range strings.Split(raw, ",") {
		s := SourceType(strings.TrimSpace(part))
		switch s {
		case SourceApple, SourceJoplin, SourceObsidian, SourceMarkdown:
			if !seen[s] {
				out = append(out, s)
				seen[s] = true
			}
		}
	}
	if len(out) == 0 {
		return []SourceType{Source()}
	}
	return out
}

// MirrorEnabled reports whether Apple Notes <-> Obsidian bidirectional
// mirror sync is turned on via mirror_apple_obsidian: true. It only takes
// effect when MirrorSourcesConfigured() also holds — every caller must
// check both before running a mirror pass.
func MirrorEnabled() bool {
	return getBool("mirror_apple_obsidian")
}

// MirrorSourcesConfigured reports whether sync_sources covers both an Apple
// Notes source and an obsidian-backed source (obsidian or markdown — they
// share one backend and both tag their cache rows "obsidian", see
// syncdispatch.SourceKey) — the minimum mirror.Sync needs to have anything
// to diff against on both sides. Lives here rather than in cmd because all
// four mirror entry points (sync, doctor, TUI, MCP) have to apply it.
func MirrorSourcesConfigured() bool {
	var hasApple, hasObsidian bool
	for _, s := range SyncSources() {
		switch s {
		case SourceApple:
			hasApple = true
		case SourceObsidian, SourceMarkdown:
			hasObsidian = true
		}
	}
	return hasApple && hasObsidian
}

// VaultPathRaw returns the vault path as stored in config (may contain ~).
func VaultPathRaw() string {
	if p := getString("vault_path"); p != "" {
		return p
	}
	return "~/Documents/Notes"
}

func VaultPath() string {
	return expandHome(VaultPathRaw())
}

// ExcludeFolders returns vault subfolder names (comma-separated in config,
// e.g. "Diary,Templates") to skip entirely during vault scans — for
// content another tool sharing this vault owns (a diaryctl "Diary" folder,
// say) that was never meant to be treated as notectl's own notes, browsed
// in its combined view, or mirrored to Apple Notes.
func ExcludeFolders() []string {
	raw := getString("exclude_folders")
	if raw == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if f := strings.TrimSpace(part); f != "" {
			out = append(out, f)
		}
	}
	return out
}

// DBPath returns the database file path: data_dir (config key, also settable
// via NOTECTL_DATA_DIR) takes precedence — points it at a user-chosen
// directory, e.g. inside iCloud Drive or Dropbox, resolved via
// coreconfig.ResolveDir — then the legacy db_path (a full file path), then
// the private default (~/.local/share/notectl).
func DBPath() string {
	if dir := getString("data_dir"); dir != "" {
		resolved, _ := coreconfig.ResolveDir("notectl", dir)
		return filepath.Join(resolved, "notes.db")
	}
	if p := getString("db_path"); p != "" {
		return expandHome(p)
	}
	dir, _ := coreconfig.ResolveDir("notectl", "")
	return filepath.Join(dir, "notes.db")
}

// Shared reports whether DBPath currently resolves to a user-configured
// directory (data_dir) rather than the tool's private default.
func Shared() bool {
	return getString("data_dir") != ""
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
	return getString("apple_folder") // optional: Apple Notes folder to sync
}

// JoplinAPIURL is the base URL of Joplin's local Data API (Options → Web
// Clipper must be enabled in Joplin for this to be reachable).
func JoplinAPIURL() string {
	if v := getString("joplin_api_url"); v != "" {
		return v
	}
	return "http://localhost:41184"
}

// JoplinToken is the Data API auth token, copied once from Joplin's Options
// → Web Clipper screen. No default — required for any Joplin source call.
func JoplinToken() string {
	return getString("joplin_token")
}

// JoplinFolder is the optional notebook (or Parent/Child nested path) to
// scope sync/list to — empty means all notebooks.
func JoplinFolder() string {
	return getString("joplin_folder")
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
	bundleBenefitID  = "de1be860-1dfc-43da-99a8-206fb2573f09"
	notectlBenefitID = "5a6a4f6d-eb02-428a-aa02-e8cd7e37a6be"
)

// IsPro reports whether a valid Pro/Bundle or notectl-only license is
// active on this machine — gates having more than one named vault.
func IsPro() bool {
	result := licensing.Result{Status: LicenseStatus(), BenefitID: LicenseBenefitID()}
	return result.Grants(notectlBenefitID, bundleBenefitID)
}

func LicenseKey() string {
	return getString("license_key")
}

func LicenseStatus() string {
	return getString("license_status")
}

func LicenseBenefitID() string {
	return getString("license_benefit_id")
}

func PolarOrgID() string {
	if v := getString("polar_org_id"); v != "" {
		return v
	}
	return licensing.DefaultOrgID
}

// SetLicense persists the license key/status/benefit to
// ~/.config/notectl/notectl.yaml.
func SetLicense(key, status, benefitID string) error {
	set("license_key", key)
	set("license_status", status)
	set("license_benefit_id", benefitID)
	dir := configDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return writeConfig(filepath.Join(dir, "notectl.yaml"))
}

// Vaults returns the configured named vaults (name -> path). Empty if the
// user has never used `notectl vault add`.
func Vaults() map[string]string {
	raw := getStringMapString("vaults")
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
	set("vaults", vaults)
	dir := configDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return writeConfig(filepath.Join(dir, "notectl.yaml"))
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
