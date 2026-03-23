package cli

import (
	"fmt"
	"io"
	"time"

	"codex-switch/internal/config"
	"codex-switch/internal/profile"
	"codex-switch/internal/quota"
	"github.com/spf13/cobra"
)

func newStatusCommand(deps Dependencies) *cobra.Command {
	var noCheck bool

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show the active profile quota details",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(deps.ConfigPath)
			if err != nil {
				return err
			}
			if cfg.ActiveProfile == "" {
				return fmt.Errorf("no active profile set")
			}

			store := profile.NewStore(deps.ProfilesDir)
			checkLive := cfg.AutoCheck && !noCheck
			var checker quotaChecker
			if checkLive {
				checker = quotaCheckerFactory()
			}

			snapshot, err := resolveSnapshot(cmd.Context(), deps, &cfg, store, cfg.ActiveProfile, checkLive, checker)
			if err != nil {
				return err
			}

			renderStatus(cmd.OutOrStdout(), cfg.ActiveProfile, snapshot)
			return nil
		},
	}
	cmd.Flags().BoolVar(&noCheck, "no-check", false, "use cached quota data")
	if deps.Stdout != nil {
		cmd.SetOut(deps.Stdout)
	}
	if deps.Stderr != nil {
		cmd.SetErr(deps.Stderr)
	}
	return cmd
}

func renderStatus(out io.Writer, name string, snapshot quota.Snapshot) {
	fmt.Fprintf(out, "Active: %s (%s)\n\n", name, displayPlan(snapshot.Plan))
	fmt.Fprintln(out, "5-hour window:")
	fmt.Fprintf(out, "  Used: %d%%\n", snapshot.PrimaryUsedPercent)
	fmt.Fprintf(out, "  Resets in: %s\n", formatResetSummary(snapshot.PrimaryResetAfter, snapshot.PrimaryResetAt))
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Weekly quota:")
	fmt.Fprintf(out, "  Used: %d%%\n", snapshot.SecondaryUsedPercent)
	fmt.Fprintf(out, "  Resets in: %s\n", formatResetSummary(snapshot.SecondaryResetAfter, snapshot.SecondaryResetAt))
	fmt.Fprintln(out, "")
	fmt.Fprintf(out, "Credits: %s\n", creditsSummary(snapshot))
}

func formatResetSummary(after time.Duration, at time.Time) string {
	relative := after
	if relative <= 0 && !at.IsZero() {
		relative = time.Until(at)
	}
	if relative < 0 {
		relative = 0
	}

	if at.IsZero() {
		return formatResetCompact(relative)
	}

	return fmt.Sprintf("%s (at %s)", formatResetCompact(relative), at.UTC().Format("2006-01-02 15:04"))
}

func creditsSummary(snapshot quota.Snapshot) string {
	if !snapshot.HasCredits {
		return "none"
	}
	if snapshot.CreditsBalance != "" {
		return snapshot.CreditsBalance
	}
	return "available"
}
