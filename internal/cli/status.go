package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"codex-switch/internal/config"
	"codex-switch/internal/profile"
	"codex-switch/internal/quota"
	"codex-switch/internal/watcher"
	"github.com/spf13/cobra"
)

type statusSummary struct {
	activeName     string
	activeSnapshot quota.Snapshot
	activeSource   quotaSource
	rows           []listRow
	totalProfiles  int
	autoCheck      bool
	checkModel     string
	watch          statusWatchSummary
	recentSwitch   statusSwitchSummary
}

type statusWatchSummary struct {
	historyAvailable   bool
	mode               string
	notify             bool
	primaryThreshold   int
	secondaryThreshold int
	runtimeActive      string
	cooldownUntil      time.Time
	lastConfirmedAt    time.Time
	lastTrigger        string
	lastActivityAt     time.Time
}

type statusSwitchSummary struct {
	found   bool
	at      time.Time
	from    string
	to      string
	trigger string
}

func newStatusCommand(deps Dependencies) *cobra.Command {
	var noCheck bool

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show active profile, quota, and watch summary",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(deps.ConfigPath)
			if err != nil {
				return err
			}
			if cfg.ActiveProfile == "" {
				return fmt.Errorf("no active profile set")
			}

			store := profile.NewStore(deps.ProfilesDir)
			summary, err := loadStatusSummary(cmd.Context(), deps, &cfg, store, !noCheck)
			if err != nil {
				return err
			}

			renderStatus(cmd.OutOrStdout(), summary)
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

func loadStatusSummary(ctx context.Context, deps Dependencies, cfg *config.Config, store profile.Store, allowLive bool) (statusSummary, error) {
	names, err := store.List()
	if err != nil {
		return statusSummary{}, err
	}

	checkLive := cfg.AutoCheck && allowLive
	rows, err := loadProfileRows(ctx, deps, cfg, store, names, checkLive, true)
	if err != nil {
		return statusSummary{}, err
	}

	activeRow, ok := findActiveRow(rows, cfg.ActiveProfile)
	if !ok {
		return statusSummary{}, fmt.Errorf("active profile %q not found", cfg.ActiveProfile)
	}

	watchState, err := loadWatchStateSummary(deps.WatchStatePath)
	if err != nil {
		return statusSummary{}, err
	}
	recentSwitch, err := loadRecentSwitchSummary(deps.WatchChecksPath)
	if err != nil {
		return statusSummary{}, err
	}

	watchProfileState := watchState.Profiles[cfg.ActiveProfile]
	watchSummary := statusWatchSummary{
		historyAvailable:   watchStateAvailable(watchState),
		mode:               "manual foreground",
		notify:             cfg.Watch.Notify,
		primaryThreshold:   cfg.Watch.PrimaryThresholdPercent,
		secondaryThreshold: cfg.Watch.SecondaryThresholdPercent,
		runtimeActive:      watchState.Runtime.ActiveProfile,
		cooldownUntil:      watchState.Runtime.CooldownUntil,
		lastConfirmedAt:    watchProfileState.LastConfirmedAt,
		lastTrigger:        watchProfileState.LastTriggerSource,
		lastActivityAt:     latestWatchActivityAt(watchState),
	}

	return statusSummary{
		activeName:     cfg.ActiveProfile,
		activeSnapshot: activeRow.snapshot,
		activeSource:   activeRow.source,
		rows:           rows,
		totalProfiles:  len(rows),
		autoCheck:      cfg.AutoCheck,
		checkModel:     cfg.CheckModel,
		watch:          watchSummary,
		recentSwitch:   recentSwitch,
	}, nil
}

func loadWatchStateSummary(path string) (watcher.WatchState, error) {
	if path == "" {
		return watcher.WatchState{Profiles: map[string]watcher.ProfileState{}}, nil
	}
	return watcher.LoadState(path)
}

func loadRecentSwitchSummary(path string) (statusSwitchSummary, error) {
	if path == "" {
		return statusSwitchSummary{}, nil
	}

	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return statusSwitchSummary{}, nil
		}
		return statusSwitchSummary{}, err
	}
	defer f.Close()

	var latest statusSwitchSummary
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var event watcher.CheckEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue
		}
		if event.Kind != "switch" || !event.Success || event.SwitchedTo == "" {
			continue
		}
		if !latest.found || event.At.After(latest.at) {
			latest = statusSwitchSummary{
				found:   true,
				at:      event.At,
				from:    event.Profile,
				to:      event.SwitchedTo,
				trigger: event.Trigger,
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return statusSwitchSummary{}, err
	}

	return latest, nil
}

func watchStateAvailable(state watcher.WatchState) bool {
	if !state.LastCleanupAt.IsZero() || !state.Runtime.CooldownUntil.IsZero() || state.Runtime.ActiveProfile != "" {
		return true
	}
	for _, profileState := range state.Profiles {
		if !profileState.LastConfirmedAt.IsZero() || !profileState.LastTriggeredAt.IsZero() {
			return true
		}
	}
	return false
}

func latestWatchActivityAt(state watcher.WatchState) time.Time {
	latest := state.LastCleanupAt
	if state.Runtime.CooldownUntil.After(latest) {
		latest = state.Runtime.CooldownUntil
	}
	for _, profileState := range state.Profiles {
		if profileState.LastConfirmedAt.After(latest) {
			latest = profileState.LastConfirmedAt
		}
		if profileState.LastTriggeredAt.After(latest) {
			latest = profileState.LastTriggeredAt
		}
	}
	return latest
}

func renderStatus(out io.Writer, summary statusSummary) {
	sections := []string{
		renderStatusHeader(summary),
		renderStatusTable(summary.rows),
		"Watch: " + renderWatchStatus(summary.watch),
		"Recent Switch: " + renderRecentSwitchStatus(summary.recentSwitch),
	}

	fmt.Fprint(out, strings.Join(sections, "\n\n"))
}

func renderStatusHeader(summary statusSummary) string {
	parts := []string{
		"ACTIVE " + summary.activeName,
		"PLAN " + displayPlan(summary.activeSnapshot.Plan),
		"SRC " + strings.ToUpper(string(summary.activeSource)),
		"PROFILES " + fmt.Sprintf("%d total", summary.totalProfiles),
		"AUTO " + formatOnOff(summary.autoCheck),
		"MODEL " + summary.checkModel,
	}
	return "Status\n" + strings.Join(parts, "   ")
}

func renderStatusTable(rows []listRow) string {
	var buf bytes.Buffer
	renderList(&buf, rows)
	return strings.TrimRight(buf.String(), "\n")
}

func renderWatchStatus(summary statusWatchSummary) string {
	parts := []string{
		"mode " + summary.mode,
		"notify " + formatYesNo(summary.notify),
		fmt.Sprintf("thresholds 5H %d%% / weekly %d%%", summary.primaryThreshold, summary.secondaryThreshold),
	}

	if !summary.historyAvailable {
		parts = append(parts, "history none")
		return strings.Join(parts, " | ")
	}

	parts = append(parts,
		"runtime active "+formatValueOrDash(summary.runtimeActive),
		"cooldown "+formatTimestampOrNone(summary.cooldownUntil),
		"last confirmed "+formatTimestampOrNone(summary.lastConfirmedAt),
		"last trigger "+formatValueOrDash(summary.lastTrigger),
		"last activity "+formatTimestampOrNone(summary.lastActivityAt),
	)
	return strings.Join(parts, " | ")
}

func renderRecentSwitchStatus(summary statusSwitchSummary) string {
	if !summary.found {
		return "No auto-switch recorded yet"
	}

	return strings.Join([]string{
		formatTimestamp(summary.at),
		"from " + summary.from,
		"to " + summary.to,
		"trigger " + formatValueOrDash(summary.trigger),
	}, " | ")
}

func formatOnOff(enabled bool) string {
	if enabled {
		return "on"
	}
	return "off"
}

func formatYesNo(enabled bool) string {
	if enabled {
		return "yes"
	}
	return "no"
}

func formatTimestampOrNone(at time.Time) string {
	if at.IsZero() {
		return "none"
	}
	return formatTimestamp(at)
}

func formatTimestamp(at time.Time) string {
	return at.UTC().Format("2006-01-02 15:04")
}

func formatValueOrDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}

func findActiveRow(rows []listRow, activeName string) (listRow, bool) {
	for _, row := range rows {
		if row.name == activeName {
			return row, true
		}
	}
	return listRow{}, false
}
