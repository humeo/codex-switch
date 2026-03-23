package cli

import (
	"errors"
	"fmt"
	"io"
	"os"

	"codex-switch/internal/config"
	"codex-switch/internal/profile"
	"codex-switch/internal/switcher"
	"github.com/spf13/cobra"
)

func newUseCommand(deps Dependencies) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "use [profile]",
		Short: "Switch the active profile",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store := profile.NewStore(deps.ProfilesDir)
			if len(args) == 1 {
				return switchProfileByName(cmd.OutOrStdout(), deps, store, args[0])
			}

			names, err := store.List()
			if err != nil {
				return err
			}
			if len(names) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), emptyProfilesMessage)
				return nil
			}

			input := deps.Stdin
			if input == nil {
				input = os.Stdin
			}
			if !useIsTerminal(input) || !useIsTerminal(outputTerminalFile(cmd.OutOrStdout())) {
				return errors.New("use without a profile name requires an interactive terminal; pass 'codex-switch use <name>'")
			}

			cfg, err := config.Load(deps.ConfigPath)
			if err != nil {
				return err
			}

			rows, err := loadProfileRows(cmd.Context(), deps, &cfg, store, names, cfg.AutoCheck, true)
			if err != nil {
				return err
			}
			name, err := useSelectProfile(input, cmd.OutOrStdout(), rows)
			if err != nil {
				if errors.Is(err, errProfileSelectionCancelled) {
					return nil
				}
				return err
			}
			return switchProfileByName(cmd.OutOrStdout(), deps, store, name)
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

func switchProfileByName(out io.Writer, deps Dependencies, store profile.Store, name string) error {
	cfg, err := switcher.SwitchProfile(deps.ConfigPath, deps.AuthPath, store, name)
	if err != nil {
		return err
	}

	fmt.Fprintf(out, "active profile: %s\n", cfg.ActiveProfile)
	return nil
}

func outputTerminalFile(out io.Writer) *os.File {
	if file, ok := out.(*os.File); ok {
		return file
	}
	return os.Stdout
}
