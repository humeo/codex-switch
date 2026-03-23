package watcher

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"codex-switch/internal/config"
	"codex-switch/internal/profile"
	"codex-switch/internal/quota"
)

const (
	defaultRetentionWindow = 7 * 24 * time.Hour
	defaultSwitchCooldown  = time.Minute
	retentionPruneInterval = 24 * time.Hour
)

type ProfileLoader interface {
	Load(name string) ([]byte, error)
}

type QuotaChecker interface {
	Check(context.Context, quota.Tokens, string) (quota.Snapshot, error)
}

type ProfileSwitcher interface {
	Switch(name string) error
}

type Notifier interface {
	Notify(title, body string) error
}

type Service struct {
	Profiles      ProfileLoader
	QuotaClient   QuotaChecker
	Switcher      ProfileSwitcher
	Notifier      Notifier
	Logger        io.Writer
	ConfigPath    string
	SessionsDir   string
	StatePath     string
	ChecksPath    string
	LogPath       string
	PollInterval  time.Duration
	Cooldown      time.Duration
	Now           func() time.Time
	TriggerEvents <-chan TokenCountEvent
}

type profileStatus struct {
	name     string
	snapshot quota.Snapshot
}

type runOutcome struct {
	active         string
	activeSnapshot quota.Snapshot
	switched       bool
}

func (s Service) RunOnce(ctx context.Context, cfg config.Config, active string, names []string) error {
	_, err := s.runCheck(ctx, cfg, active, names, "active_check", "manual", nil)
	return err
}

func (s Service) Run(ctx context.Context, cfg config.Config, active string, names []string) error {
	if active == "" {
		return nil
	}
	if s.Profiles == nil {
		return errors.New("watcher: profile loader is required")
	}
	if s.QuotaClient == nil {
		return errors.New("watcher: quota client is required")
	}

	now := s.now()
	state, err := s.loadState()
	if err != nil {
		return err
	}
	state.Runtime.ActiveProfile = active
	if err := s.maybePruneRetention(&state, now, true); err != nil {
		return err
	}

	s.writeLine("watching local Codex session events")

	startup, err := s.runCheck(ctx, cfg, active, names, "startup_calibration", "startup_calibration", &state)
	if err != nil {
		return err
	}
	active = startup.active
	state.Runtime.ActiveProfile = active
	if startup.switched {
		state.Runtime.CooldownUntil = now.Add(s.cooldown())
	}
	if err := s.saveState(state); err != nil {
		return err
	}
	s.writeLine(fmt.Sprintf(
		"startup calibration complete for %s: primary %d%%, weekly %d%%",
		active,
		startup.activeSnapshot.PrimaryUsedPercent,
		startup.activeSnapshot.SecondaryUsedPercent,
	))

	events, errs := s.eventStream(ctx)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err, ok := <-errs:
			if !ok {
				errs = nil
				continue
			}
			if err != nil {
				return err
			}
		case event, ok := <-events:
			if !ok {
				return nil
			}

			if s.inCooldown(state, s.now()) {
				continue
			}
			if !event.Exceeds(cfg) {
				continue
			}

			active, err = s.reloadActiveProfile(active)
			if err != nil {
				return err
			}
			state.Runtime.ActiveProfile = active

			s.writeLine(fmt.Sprintf("session trigger exceeded threshold; confirming active profile %s", active))

			outcome, err := s.runCheck(ctx, cfg, active, names, "active_check", "session_rate_limits", &state)
			if err != nil {
				return err
			}
			active = outcome.active
			state.Runtime.ActiveProfile = active
			if outcome.switched {
				state.Runtime.CooldownUntil = s.now().Add(s.cooldown())
			}
			if err := s.maybePruneRetention(&state, s.now(), false); err != nil {
				return err
			}
			if err := s.saveState(state); err != nil {
				return err
			}
		}
	}
}

func (s Service) runCheck(ctx context.Context, cfg config.Config, active string, names []string, kind, trigger string, state *WatchState) (runOutcome, error) {
	activeSnapshot, err := s.snapshotForName(ctx, cfg, active)
	if err != nil {
		return runOutcome{}, err
	}
	s.recordProfileState(state, active, activeSnapshot, trigger, s.now())
	if err := s.appendCheck(CheckEvent{
		At:                   s.now(),
		Profile:              active,
		Kind:                 kind,
		Trigger:              trigger,
		Success:              true,
		PlanType:             activeSnapshot.Plan,
		PrimaryUsedPercent:   activeSnapshot.PrimaryUsedPercent,
		SecondaryUsedPercent: activeSnapshot.SecondaryUsedPercent,
		EstimatedTokens:      19,
	}); err != nil {
		return runOutcome{}, err
	}

	outcome := runOutcome{
		active:         active,
		activeSnapshot: activeSnapshot,
	}
	if !thresholdReached(activeSnapshot, cfg) {
		return outcome, nil
	}

	seen := map[string]struct{}{active: {}}
	candidates := make([]profileStatus, 0, len(names))
	for _, name := range names {
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}

		snapshot, err := s.snapshotForName(ctx, cfg, name)
		if err != nil {
			return runOutcome{}, err
		}
		s.recordProfileState(state, name, snapshot, "candidate_check", s.now())
		if err := s.appendCheck(CheckEvent{
			At:                   s.now(),
			Profile:              name,
			Kind:                 "candidate_check",
			Trigger:              trigger,
			Success:              true,
			PlanType:             snapshot.Plan,
			PrimaryUsedPercent:   snapshot.PrimaryUsedPercent,
			SecondaryUsedPercent: snapshot.SecondaryUsedPercent,
			EstimatedTokens:      19,
		}); err != nil {
			return runOutcome{}, err
		}
		candidates = append(candidates, profileStatus{name: name, snapshot: snapshot})
	}

	if len(candidates) == 0 {
		s.reportDepleted(active)
		return outcome, nil
	}

	best := chooseBestCandidate(candidates)
	if best.snapshot.SecondaryUsedPercent >= activeSnapshot.SecondaryUsedPercent {
		s.reportDepleted(active)
		return outcome, nil
	}
	if s.Switcher == nil {
		return runOutcome{}, errors.New("watcher: switcher is required")
	}
	if err := s.Switcher.Switch(best.name); err != nil {
		return runOutcome{}, err
	}
	if err := s.appendCheck(CheckEvent{
		At:         s.now(),
		Profile:    active,
		Kind:       "switch",
		Trigger:    trigger,
		Success:    true,
		SwitchedTo: best.name,
	}); err != nil {
		return runOutcome{}, err
	}

	s.reportSwitch(active, best.name)
	outcome.active = best.name
	outcome.switched = true
	return outcome, nil
}

func (s Service) eventStream(ctx context.Context) (<-chan TokenCountEvent, <-chan error) {
	if s.TriggerEvents != nil {
		errs := make(chan error)
		close(errs)
		return s.TriggerEvents, errs
	}

	monitor := SessionMonitor{
		Root:         s.SessionsDir,
		PollInterval: s.PollInterval,
	}
	return monitor.Stream(ctx)
}

func (s Service) snapshotForName(ctx context.Context, cfg config.Config, name string) (quota.Snapshot, error) {
	raw, err := s.Profiles.Load(name)
	if err != nil {
		return quota.Snapshot{}, err
	}

	tokens, err := tokensFromProfile(raw)
	if err != nil {
		return quota.Snapshot{}, err
	}

	return s.QuotaClient.Check(ctx, tokens, cfg.CheckModel)
}

func tokensFromProfile(raw []byte) (quota.Tokens, error) {
	var stored profile.Profile
	if err := json.Unmarshal(raw, &stored); err != nil {
		return quota.Tokens{}, err
	}
	if stored.Tokens.AccessToken == "" {
		return quota.Tokens{}, errors.New("watcher: profile is missing access token")
	}

	return quota.Tokens{
		AccessToken: stored.Tokens.AccessToken,
		AccountID:   stored.Tokens.AccountID,
	}, nil
}

func thresholdReached(snapshot quota.Snapshot, cfg config.Config) bool {
	return snapshot.PrimaryUsedPercent >= cfg.Watch.PrimaryThresholdPercent ||
		snapshot.SecondaryUsedPercent >= cfg.Watch.SecondaryThresholdPercent
}

func chooseBestCandidate(candidates []profileStatus) profileStatus {
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].snapshot.SecondaryUsedPercent != candidates[j].snapshot.SecondaryUsedPercent {
			return candidates[i].snapshot.SecondaryUsedPercent < candidates[j].snapshot.SecondaryUsedPercent
		}
		if candidates[i].snapshot.PrimaryUsedPercent != candidates[j].snapshot.PrimaryUsedPercent {
			return candidates[i].snapshot.PrimaryUsedPercent < candidates[j].snapshot.PrimaryUsedPercent
		}
		return candidates[i].name < candidates[j].name
	})
	return candidates[0]
}

func (s Service) reportSwitch(from, to string) {
	s.writeLine(fmt.Sprintf("switching from %s to %s", from, to))
	s.notify("codex-switch", fmt.Sprintf("Switched from %s to %s", from, to))
}

func (s Service) reportDepleted(active string) {
	msg := fmt.Sprintf("All accounts depleted; no better profile than %s", active)
	s.writeLine(msg)
	s.notify("codex-switch", msg)
}

func (s Service) writeLine(msg string) {
	if s.Logger != nil {
		if !strings.HasSuffix(msg, "\n") {
			msg += "\n"
		}
		_, _ = io.WriteString(s.Logger, msg)
	}
	_ = AppendLogLine(s.LogPath, s.now(), msg)
}

func (s Service) notify(title, body string) {
	if s.Notifier == nil {
		return
	}
	_ = s.Notifier.Notify(title, body)
}

func (s Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now().UTC()
}

func (s Service) cooldown() time.Duration {
	if s.Cooldown > 0 {
		return s.Cooldown
	}
	return defaultSwitchCooldown
}

func (s Service) inCooldown(state WatchState, now time.Time) bool {
	return !state.Runtime.CooldownUntil.IsZero() && now.Before(state.Runtime.CooldownUntil)
}

func (s Service) loadState() (WatchState, error) {
	if s.StatePath == "" {
		return WatchState{Profiles: map[string]ProfileState{}}, nil
	}
	return LoadState(s.StatePath)
}

func (s Service) saveState(state WatchState) error {
	if s.StatePath == "" {
		return nil
	}
	return SaveState(s.StatePath, state)
}

func (s Service) pruneRetention(now time.Time) error {
	cutoff := now.Add(-defaultRetentionWindow)
	if err := PruneJSONLFile(s.ChecksPath, cutoff); err != nil {
		return err
	}
	if err := PruneLogFile(s.LogPath, cutoff); err != nil {
		return err
	}
	return nil
}

func (s Service) maybePruneRetention(state *WatchState, now time.Time, force bool) error {
	if state == nil {
		return nil
	}
	if !force && !state.LastCleanupAt.IsZero() && now.Sub(state.LastCleanupAt) < retentionPruneInterval {
		return nil
	}
	if err := s.pruneRetention(now); err != nil {
		return err
	}
	state.LastCleanupAt = now
	return nil
}

func (s Service) appendCheck(event CheckEvent) error {
	if s.ChecksPath == "" {
		return nil
	}
	return AppendCheckEvent(s.ChecksPath, event)
}

func (s Service) reloadActiveProfile(active string) (string, error) {
	if s.ConfigPath == "" {
		return active, nil
	}
	cfg, err := config.Load(s.ConfigPath)
	if err != nil {
		return "", err
	}
	if cfg.ActiveProfile == "" {
		return active, nil
	}
	return cfg.ActiveProfile, nil
}

func (s Service) recordProfileState(state *WatchState, name string, snapshot quota.Snapshot, trigger string, now time.Time) {
	if state == nil {
		return
	}
	if state.Profiles == nil {
		state.Profiles = map[string]ProfileState{}
	}

	profileState := state.Profiles[name]
	profileState.LastConfirmedAt = now
	profileState.LastTriggeredAt = now
	profileState.LastTriggerSource = trigger
	profileState.LastPlan = snapshot.Plan
	profileState.LastPrimaryUsedPercent = snapshot.PrimaryUsedPercent
	profileState.LastSecondaryUsedPercent = snapshot.SecondaryUsedPercent
	profileState.LastPrimaryResetAt = snapshot.PrimaryResetAt
	profileState.LastSecondaryResetAt = snapshot.SecondaryResetAt
	profileState.Samples = append(profileState.Samples, SnapshotSample{
		At:                   now,
		PrimaryUsedPercent:   snapshot.PrimaryUsedPercent,
		SecondaryUsedPercent: snapshot.SecondaryUsedPercent,
	})
	if len(profileState.Samples) > maxSnapshotSamples {
		profileState.Samples = profileState.Samples[len(profileState.Samples)-maxSnapshotSamples:]
	}
	state.Profiles[name] = profileState
}
