package cmd

import (
	"github.com/aeon022/notectl/internal/config"
	"github.com/aeon022/notectl/internal/notes"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "notectl",
	Short: "Notes from the terminal — Obsidian vault, Apple Notes, or Joplin",
}

func Execute() error {
	config.Init()
	notes.SetJoplinConfigFuncs(config.JoplinAPIURL, config.JoplinToken)
	return rootCmd.Execute()
}
