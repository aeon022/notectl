package cmd

import (
	"github.com/aeon022/notectl/internal/mcpserver"
	"github.com/spf13/cobra"
)

var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "Start MCP server (stdio) — exposes list, read, write, search, sync as AI tools",
	RunE: func(cmd *cobra.Command, args []string) error {
		return mcpserver.Serve()
	},
}

func init() { rootCmd.AddCommand(mcpCmd) }
