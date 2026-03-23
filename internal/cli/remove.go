package cli

import (
	"fmt"

	"codex-switch/internal/profile"
	"codex-switch/internal/switcher"
	"github.com/spf13/cobra"
)

func newRemoveCommand(deps Dependencies) *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:   "remove <profile>",
		Short: "Remove a stored profile",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store := profile.NewStore(deps.ProfilesDir)
			if err := switcher.RemoveProfile(deps.ConfigPath, store, args[0], force); err != nil {
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(), "removed profile: %s\n", args[0])
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "remove an active profile")
	if deps.Stdout != nil {
		cmd.SetOut(deps.Stdout)
	}
	if deps.Stderr != nil {
		cmd.SetErr(deps.Stderr)
	}
	return cmd
}
