package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/viper"
)

// SourceType identifies the notes backend.
type SourceType string

const (
	SourceObsidian SourceType = "obsidian"
	SourceJoplin   SourceType = "joplin"
	SourceMarkdown SourceType = "markdown"
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
	case SourceJoplin, SourceMarkdown:
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

func DBPath() string {
	if p := viper.GetString("db_path"); p != "" {
		return expandHome(p)
	}
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".local", "share", "notectl")
	_ = os.MkdirAll(dir, 0o755)
	return filepath.Join(dir, "notes.db")
}

func AppleFolder() string {
	return viper.GetString("apple_folder") // optional: Apple Notes folder to sync
}

func configDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "notectl")
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
