package cli

import (
	"context"
	"fmt"
	"time"

	"codex-switch/internal/config"
	"codex-switch/internal/profile"
	"codex-switch/internal/quota"
	"codex-switch/internal/switcher"
	"codex-switch/internal/watcher"
	"github.com/gen2brain/beeep"
	"github.com/spf13/cobra"
)

type beeepNotifier struct{}

func (beeepNotifier) Notify(title, body string) error {
	return beeep.Notify(title, body, nil)
}

type noopNotifier struct{}

func (noopNotifier) Notify(string, string) error { return nil }

type profileSwitcherAdapter struct {
	cfgPath  string
	authPath string
	store    profile.Store
}

func (a profileSwitcherAdapter) Switch(name string) error {
	_, err := switcher.SwitchProfile(a.cfgPath, a.authPath, a.store, name)
	return err
}

type liveWatcherQuotaChecker struct{}

func (liveWatcherQuotaChecker) Check(ctx context.Context, tokens quota.Tokens, model string) (quota.Snapshot, error) {
	return quota.Client{}.Check(ctx, tokens, model)
}

func newWatchCommand(deps Dependencies) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "watch",
		Short: "Watch quota usage and switch profiles automatically",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(deps.ConfigPath)
			if err != nil {
				return err
			}
			if cfg.ActiveProfile == "" {
				return fmt.Errorf("no active profile set")
			}

			store := profile.NewStore(deps.ProfilesDir)
			names, err := store.List()
			if err != nil {
				return err
			}

			interval := time.Duration(cfg.Watch.IntervalMinutes) * time.Minute
			if interval <= 0 {
				interval = 5 * time.Minute
			}
			fmt.Fprintf(cmd.OutOrStdout(), "watching every %s; primary >= %d%% or weekly >= %d%%\n",
				interval,
				cfg.Watch.PrimaryThresholdPercent,
				cfg.Watch.SecondaryThresholdPercent,
			)

			var notifier watcher.Notifier
			if cfg.Watch.Notify {
				notifier = beeepNotifier{}
			} else {
				notifier = noopNotifier{}
			}

			service := watcher.Service{
				Profiles:    store,
				QuotaClient: liveWatcherQuotaChecker{},
				Switcher: profileSwitcherAdapter{
					cfgPath:  deps.ConfigPath,
					authPath: deps.AuthPath,
					store:    store,
				},
				Notifier: notifier,
				Logger:   cmd.OutOrStdout(),
			}

			return service.Run(cmd.Context(), cfg, cfg.ActiveProfile, names)
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
