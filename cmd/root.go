package cmd

import (
	"github.com/aeon022/notectl/internal/config"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "notectl",
	Short: "Notes from the terminal — Obsidian vault + Apple Notes",
}

func Execute() error {
	config.Init()
	return rootCmd.Execute()
}
