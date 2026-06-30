package config

import (
	"os"
	"path/filepath"

	"github.com/spf13/viper"
)

func Init() {
	viper.SetConfigName("notectl")
	viper.SetConfigType("yaml")
	viper.AddConfigPath("$HOME/.config/notectl")
	viper.AddConfigPath(".")
	viper.SetEnvPrefix("NOTECTL")
	viper.AutomaticEnv()
	_ = viper.ReadInConfig()
}

func VaultPath() string {
	if p := viper.GetString("vault_path"); p != "" {
		return expandHome(p)
	}
	// default: ~/Documents/Notes
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Documents", "Notes")
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

func expandHome(p string) string {
	if len(p) >= 2 && p[:2] == "~/" {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, p[2:])
	}
	return p
}
