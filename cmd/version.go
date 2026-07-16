package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

// Version is set at build time via -ldflags "-X github.com/aeon022/notectl/cmd.Version=v1.2.3".
var Version = "dev"

func init() {
	rootCmd.Version = Version
	rootCmd.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print version",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("notectl %s\n", Version)
		},
	})
}
