package cmd

import (
	"fmt"

	"github.com/aeon022/notectl/internal/config"
	"github.com/spf13/cobra"
)

var vaultCmd = &cobra.Command{
	Use:   "vault",
	Short: "Manage multiple named vaults (missionctl Bundle: more than one)",
}

var vaultAddCmd = &cobra.Command{
	Use:   "add <name> <path>",
	Short: "Register a named vault",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		name, path := args[0], args[1]
		if err := config.VaultAdd(name, path); err != nil {
			return err
		}
		fmt.Printf("✓ Vault %q added: %s\n", name, path)
		fmt.Printf("  Switch to it with: notectl vault use %s\n", name)
		return nil
	},
}

var vaultListCmd = &cobra.Command{
	Use:   "list",
	Short: "List registered named vaults",
	RunE: func(cmd *cobra.Command, args []string) error {
		vaults := config.Vaults()
		if len(vaults) == 0 {
			fmt.Println("No named vaults yet. Current vault (vault_path):", config.VaultPath())
			fmt.Println("Register one with: notectl vault add <name> <path>")
			return nil
		}
		active := config.VaultPathRaw()
		for name, path := range vaults {
			marker := "  "
			if config.VaultPath() == path || path == active {
				marker = "* "
			}
			fmt.Printf("%s%-16s %s\n", marker, name, path)
		}
		return nil
	},
}

var vaultUseCmd = &cobra.Command{
	Use:   "use <name>",
	Short: "Switch the active vault",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := config.VaultUse(args[0]); err != nil {
			return err
		}
		fmt.Printf("✓ Active vault: %s (%s)\n", args[0], config.VaultPath())
		return nil
	},
}

func init() {
	vaultCmd.AddCommand(vaultAddCmd, vaultListCmd, vaultUseCmd)
	rootCmd.AddCommand(vaultCmd)
}
