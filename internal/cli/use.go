package cli

import (
	"fmt"

	"codex-switch/internal/profile"
	"codex-switch/internal/switcher"
	"github.com/spf13/cobra"
)

func newUseCommand(deps Dependencies) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "use <profile>",
		Short: "Switch the active profile",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store := profile.NewStore(deps.ProfilesDir)
			cfg, err := switcher.SwitchProfile(deps.ConfigPath, deps.AuthPath, store, args[0])
			if err != nil {
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(), "active profile: %s\n", cfg.ActiveProfile)
			return nil
		},
	}
	if deps.Stdout != nil {
		cmd.SetOut(deps.Stdout)
	}
	if deps.Stderr != nil {
		cmd.SetErr(deps.Stderr)
	}
	return cmd
}
