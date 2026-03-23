package cli

import (
	"bufio"
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
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
)

type statusSummary struct {
	activeName     string
	activeSnapshot quota.Snapshot
	activeSource   quotaSource
	totalProfiles  int
	autoCheck      bool
	checkModel     string
	watch          statusWatchSummary
	recentSwitch   statusSwitchSummary
	previousName   string
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
	var checker quotaChecker
	if checkLive {
		checker = quotaCheckerFactory()
	}

	snapshot, err := resolveSnapshot(ctx, deps, cfg, store, cfg.ActiveProfile, checkLive, checker)
	if err != nil {
		return statusSummary{}, err
	}

	watchState, err := loadWatchStateSummary(deps.WatchStatePath)
	if err != nil {
		return statusSummary{}, err
	}
	recentSwitch, err := loadRecentSwitchSummary(deps.WatchChecksPath)
	if err != nil {
		return statusSummary{}, err
	}

	previousName := ""
	if recentSwitch.found && recentSwitch.to == cfg.ActiveProfile {
		previousName = recentSwitch.from
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

	source := quotaSourceCache
	if checkLive {
		source = quotaSourceLive
	}

	return statusSummary{
		activeName:     cfg.ActiveProfile,
		activeSnapshot: snapshot,
		activeSource:   source,
		totalProfiles:  len(names),
		autoCheck:      cfg.AutoCheck,
		checkModel:     cfg.CheckModel,
		watch:          watchSummary,
		recentSwitch:   recentSwitch,
		previousName:   previousName,
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
		renderStatusCard("Quota", renderStatusRows([]statusField{
			{label: "5H used", value: fmt.Sprintf("%d%%", summary.activeSnapshot.PrimaryUsedPercent)},
			{label: "5H left", value: fmt.Sprintf("%d%%", remainingPercent(summary.activeSnapshot.PrimaryUsedPercent))},
			{label: "5H reset", value: formatResetSummary(summary.activeSnapshot.PrimaryResetAfter, summary.activeSnapshot.PrimaryResetAt)},
			{label: "Weekly used", value: fmt.Sprintf("%d%%", summary.activeSnapshot.SecondaryUsedPercent)},
			{label: "Weekly left", value: fmt.Sprintf("%d%%", remainingPercent(summary.activeSnapshot.SecondaryUsedPercent))},
			{label: "Weekly reset", value: formatResetSummary(summary.activeSnapshot.SecondaryResetAfter, summary.activeSnapshot.SecondaryResetAt)},
			{label: "Credits", value: creditsSummary(summary.activeSnapshot)},
		})),
		renderStatusCard("Watch", renderWatchStatus(summary.watch)),
		renderStatusCard("Recent Switch", renderRecentSwitchStatus(summary.recentSwitch)),
		renderStatusCard("Profiles", renderStatusRows([]statusField{
			{label: "Current", value: summary.activeName},
			{label: "Previous", value: formatValueOrDash(summary.previousName)},
			{label: "Saved", value: fmt.Sprintf("%d total", summary.totalProfiles)},
			{label: "Auto check", value: formatOnOff(summary.autoCheck)},
			{label: "Check model", value: summary.checkModel},
		})),
	}

	fmt.Fprint(out, strings.Join(sections, "\n\n"))
}

type statusField struct {
	label string
	value string
}

var (
	statusTitleStyle  = lipgloss.NewStyle().Bold(true)
	statusMutedColor  = lipgloss.AdaptiveColor{Light: "#57606A", Dark: "#9AA4BF"}
	statusBorderColor = lipgloss.AdaptiveColor{Light: "#D0D7DE", Dark: "#434C63"}
	statusAccentColor = lipgloss.AdaptiveColor{Light: "#0969DA", Dark: "#8FB3FF"}
	statusPillStyle   = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(statusBorderColor).
				Padding(0, 1).
				MarginRight(1)
	statusPillLabelStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(statusAccentColor)
	statusPillValueStyle = lipgloss.NewStyle()
	statusCardStyle      = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(statusBorderColor).
				Padding(0, 1)
	statusCardTitleStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(statusAccentColor)
	statusFieldLabelStyle = lipgloss.NewStyle().
				Foreground(statusMutedColor)
	statusEmptyStateStyle = lipgloss.NewStyle().
				Foreground(statusMutedColor).
				Italic(true)
)

func renderStatusHeader(summary statusSummary) string {
	pills := []string{
		renderStatusPill("ACTIVE", summary.activeName),
		renderStatusPill("PLAN", displayPlan(summary.activeSnapshot.Plan)),
		renderStatusPill("SRC", strings.ToUpper(string(summary.activeSource))),
		renderStatusPill("PROFILES", fmt.Sprintf("%d total", summary.totalProfiles)),
	}

	return statusTitleStyle.Render("Status") + "\n" + lipgloss.JoinHorizontal(lipgloss.Top, pills...)
}

func renderStatusPill(label, value string) string {
	content := statusPillLabelStyle.Render(label) + " " + statusPillValueStyle.Render(value)
	return statusPillStyle.Render(content)
}

func renderStatusCard(title, body string) string {
	return statusCardStyle.Render(statusCardTitleStyle.Render(title) + "\n\n" + body)
}

func renderStatusRows(rows []statusField) string {
	maxWidth := 0
	for _, row := range rows {
		if len(row.label) > maxWidth {
			maxWidth = len(row.label)
		}
	}

	lines := make([]string, 0, len(rows))
	for _, row := range rows {
		lines = append(lines, statusFieldLabelStyle.Width(maxWidth).Render(row.label)+"  "+row.value)
	}
	return strings.Join(lines, "\n")
}

func renderWatchStatus(summary statusWatchSummary) string {
	rows := []statusField{
		{label: "Mode", value: summary.mode},
		{label: "Notify", value: formatYesNo(summary.notify)},
		{label: "Thresholds", value: fmt.Sprintf("5H %d%% / weekly %d%%", summary.primaryThreshold, summary.secondaryThreshold)},
	}

	if !summary.historyAvailable {
		rows = append(rows, statusField{label: "History", value: statusEmptyStateStyle.Render("No watch history yet")})
		return renderStatusRows(rows)
	}

	rows = append(rows,
		statusField{label: "Runtime active", value: formatValueOrDash(summary.runtimeActive)},
		statusField{label: "Cooldown", value: formatTimestampOrNone(summary.cooldownUntil)},
		statusField{label: "Last confirmed", value: formatTimestampOrNone(summary.lastConfirmedAt)},
		statusField{label: "Last trigger", value: formatValueOrDash(summary.lastTrigger)},
		statusField{label: "Last activity", value: formatTimestampOrNone(summary.lastActivityAt)},
	)
	return renderStatusRows(rows)
}

func renderRecentSwitchStatus(summary statusSwitchSummary) string {
	if !summary.found {
		return statusEmptyStateStyle.Render("No auto-switch recorded yet")
	}

	return renderStatusRows([]statusField{
		{label: "Last auto switch", value: formatTimestamp(summary.at)},
		{label: "From", value: summary.from},
		{label: "To", value: summary.to},
		{label: "Trigger", value: formatValueOrDash(summary.trigger)},
	})
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
