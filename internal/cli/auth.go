package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"codex-switch/internal/config"
	"codex-switch/internal/profile"
	"github.com/spf13/cobra"
)

type Runner interface {
	Run(ctx context.Context, name string, args ...string) error
}

type execRunner struct{}

func (execRunner) Run(ctx context.Context, name string, args ...string) error {
	return exec.CommandContext(ctx, name, args...).Run()
}

var authRunnerFactory = func() Runner {
	return execRunner{}
}

func newAuthCommand(deps Dependencies) *cobra.Command {
	var overwrite bool

	cmd := &cobra.Command{
		Use:   "auth <name>",
		Short: "Capture a new Codex auth profile",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) (err error) {
			name := args[0]
			store := profile.NewStore(deps.ProfilesDir)

			cfg, err := config.Load(deps.ConfigPath)
			if err != nil {
				return err
			}
			existingProfiles, err := store.List()
			if err != nil {
				return err
			}
			firstProfile := len(existingProfiles) == 0

			if _, err := store.Load(name); err == nil && !overwrite {
				return fmt.Errorf("profile %q already exists; use --overwrite to replace it", name)
			} else if err != nil && !os.IsNotExist(err) {
				return err
			}

			restore := func() error { return nil }
			if _, err := os.Stat(deps.AuthPath); err == nil {
				backupPath := deps.AuthPath + ".bak"
				_ = os.Remove(backupPath)
				if err := os.Rename(deps.AuthPath, backupPath); err != nil {
					return err
				}
				restore = func() error {
					_ = os.Remove(deps.AuthPath)
					if err := os.Rename(backupPath, deps.AuthPath); err != nil {
						return err
					}
					return nil
				}
			} else if !os.IsNotExist(err) {
				return err
			} else {
				restore = func() error {
					_ = os.Remove(deps.AuthPath)
					return nil
				}
			}

			defer func() {
				if restoreErr := restore(); restoreErr != nil && err == nil {
					err = restoreErr
				}
			}()

			runner := authRunnerFactory()
			fmt.Fprintln(cmd.ErrOrStderr(), "warning: codex logout and codex login will run now")
			if err := runner.Run(cmd.Context(), "codex", "logout"); err != nil {
				return err
			}
			if err := runner.Run(cmd.Context(), "codex", "login"); err != nil {
				return err
			}

			raw, err := os.ReadFile(deps.AuthPath)
			if err != nil {
				return err
			}
			if err := store.Save(name, raw); err != nil {
				return err
			}

			if firstProfile {
				cfg.ActiveProfile = name
				if err := config.Save(deps.ConfigPath, cfg); err != nil {
					return err
				}
			}

			fmt.Fprintf(cmd.OutOrStdout(), "saved profile: %s\n", name)
			return nil
		},
	}

	cmd.Flags().BoolVar(&overwrite, "overwrite", false, "replace an existing profile")
	if deps.Stdout != nil {
		cmd.SetOut(deps.Stdout)
	}
	if deps.Stderr != nil {
		cmd.SetErr(deps.Stderr)
	}
	return cmd
}
