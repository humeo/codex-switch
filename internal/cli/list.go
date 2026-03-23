package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"text/tabwriter"
	"time"

	"codex-switch/internal/config"
	"codex-switch/internal/profile"
	"codex-switch/internal/quota"
	"github.com/spf13/cobra"
)

const emptyProfilesMessage = "No profiles found. Run 'codex-switch auth --login <name>' to add one."

type quotaChecker interface {
	Check(context.Context, quota.Tokens, string) (quota.Snapshot, error)
}

type quotaCheckerFunc func(context.Context, quota.Tokens, string) (quota.Snapshot, error)

func (f quotaCheckerFunc) Check(ctx context.Context, tokens quota.Tokens, model string) (quota.Snapshot, error) {
	return f(ctx, tokens, model)
}

type liveQuotaChecker struct{}

func (liveQuotaChecker) Check(ctx context.Context, tokens quota.Tokens, model string) (quota.Snapshot, error) {
	return quota.Client{}.Check(ctx, tokens, model)
}

var quotaCheckerFactory = func() quotaChecker {
	return liveQuotaChecker{}
}

type listRow struct {
	name     string
	snapshot quota.Snapshot
	active   bool
	source   quotaSource
}

func newListCommand(deps Dependencies) *cobra.Command {
	var noCheck bool

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List stored profiles and quota status",
		RunE: func(cmd *cobra.Command, args []string) error {
			store := profile.NewStore(deps.ProfilesDir)

			cfg, err := config.Load(deps.ConfigPath)
			if err != nil {
				return err
			}

			names, err := store.List()
			if err != nil {
				return err
			}
			if len(names) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), emptyProfilesMessage)
				return nil
			}

			rows, err := loadProfileRows(cmd.Context(), deps, &cfg, store, names, cfg.AutoCheck && !noCheck, true)
			if err != nil {
				return err
			}

			renderList(cmd.OutOrStdout(), rows)
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

func resolveSnapshot(ctx context.Context, deps Dependencies, cfg *config.Config, store profile.Store, name string, checkLive bool, checker quotaChecker) (quota.Snapshot, error) {
	if checkLive {
		if checker == nil {
			checker = quotaCheckerFactory()
		}

		raw, err := store.Load(name)
		if err != nil {
			return quota.Snapshot{}, err
		}

		tokens, err := tokensFromProfile(raw)
		if err != nil {
			return quota.Snapshot{}, err
		}

		snapshot, err := checker.Check(ctx, tokens, cfg.CheckModel)
		if err != nil {
			return quota.Snapshot{}, err
		}

		if cfg.Cache == nil {
			cfg.Cache = map[string]config.QuotaCache{}
		}
		cfg.Cache[name] = cacheFromSnapshot(snapshot)
		if err := config.Save(deps.ConfigPath, *cfg); err != nil {
			return quota.Snapshot{}, err
		}

		return snapshot, nil
	}

	return snapshotFromCache(cfg.Cache[name]), nil
}

func tokensFromProfile(raw []byte) (quota.Tokens, error) {
	var stored profile.Profile
	if err := json.Unmarshal(raw, &stored); err != nil {
		return quota.Tokens{}, err
	}

	return quota.Tokens{
		AccessToken: stored.Tokens.AccessToken,
		AccountID:   stored.Tokens.AccountID,
	}, nil
}

func cacheFromSnapshot(snapshot quota.Snapshot) config.QuotaCache {
	return config.QuotaCache{
		Plan:                       snapshot.Plan,
		PrimaryUsedPercent:         snapshot.PrimaryUsedPercent,
		SecondaryUsedPercent:       snapshot.SecondaryUsedPercent,
		PrimaryResetAfterSeconds:   int64(snapshot.PrimaryResetAfter / time.Second),
		SecondaryResetAfterSeconds: int64(snapshot.SecondaryResetAfter / time.Second),
		PrimaryResetAt:             snapshot.PrimaryResetAt,
		SecondaryResetAt:           snapshot.SecondaryResetAt,
		HasCredits:                 snapshot.HasCredits,
		CreditsBalance:             snapshot.CreditsBalance,
	}
}

func snapshotFromCache(cache config.QuotaCache) quota.Snapshot {
	return quota.Snapshot{
		Plan:                 cache.Plan,
		PrimaryUsedPercent:   cache.PrimaryUsedPercent,
		SecondaryUsedPercent: cache.SecondaryUsedPercent,
		PrimaryResetAfter:    time.Duration(cache.PrimaryResetAfterSeconds) * time.Second,
		SecondaryResetAfter:  time.Duration(cache.SecondaryResetAfterSeconds) * time.Second,
		PrimaryResetAt:       cache.PrimaryResetAt,
		SecondaryResetAt:     cache.SecondaryResetAt,
		HasCredits:           cache.HasCredits,
		CreditsBalance:       cache.CreditsBalance,
	}
}

func renderList(out io.Writer, rows []listRow) {
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tPLAN\t5H USED\t5H LEFT\tWEEKLY USED\tWEEKLY LEFT\t5H RESET\tWEEKLY RESET\tSRC\tACTIVE")
	for _, row := range rows {
		fmt.Fprintf(w, "%s\t%s\t%d%%\t%d%%\t%d%%\t%d%%\t%s\t%s\t%s\t%s\n",
			row.name,
			displayPlan(row.snapshot.Plan),
			row.snapshot.PrimaryUsedPercent,
			remainingPercent(row.snapshot.PrimaryUsedPercent),
			row.snapshot.SecondaryUsedPercent,
			remainingPercent(row.snapshot.SecondaryUsedPercent),
			formatResetCompact(row.snapshot.PrimaryResetAfter),
			formatResetCompact(row.snapshot.SecondaryResetAfter),
			string(row.source),
			activeMarker(row.active),
		)
	}
	_ = w.Flush()
}

func sortListRows(rows []listRow) {
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].snapshot.SecondaryUsedPercent != rows[j].snapshot.SecondaryUsedPercent {
			return rows[i].snapshot.SecondaryUsedPercent < rows[j].snapshot.SecondaryUsedPercent
		}
		if rows[i].snapshot.PrimaryUsedPercent != rows[j].snapshot.PrimaryUsedPercent {
			return rows[i].snapshot.PrimaryUsedPercent < rows[j].snapshot.PrimaryUsedPercent
		}
		return rows[i].name < rows[j].name
	})
}

func activeMarker(active bool) string {
	if active {
		return "*"
	}
	return ""
}

func displayPlan(plan string) string {
	if plan == "" {
		return "unknown"
	}
	return plan
}

func formatResetCompact(d time.Duration) string {
	if d <= 0 {
		return "0m"
	}

	days := d / (24 * time.Hour)
	d -= days * 24 * time.Hour
	hours := d / time.Hour
	d -= hours * time.Hour
	minutes := d / time.Minute

	switch {
	case days > 0:
		if hours > 0 {
			return fmt.Sprintf("%dd %dh", days, hours)
		}
		return fmt.Sprintf("%dd", days)
	case hours > 0:
		if minutes > 0 {
			return fmt.Sprintf("%dh %dm", hours, minutes)
		}
		return fmt.Sprintf("%dh", hours)
	default:
		if minutes > 0 {
			return fmt.Sprintf("%dm", minutes)
		}
		return "0m"
	}
}
