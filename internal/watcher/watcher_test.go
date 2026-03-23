package watcher

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex-switch/internal/config"
	"codex-switch/internal/quota"
)

type fakeProfileLoader struct {
	raw map[string][]byte
}

func (f fakeProfileLoader) Load(name string) ([]byte, error) {
	raw, ok := f.raw[name]
	if !ok {
		return nil, fmt.Errorf("missing profile %q", name)
	}
	return raw, nil
}

type fakeQuotaChecker struct {
	snapshots map[string]quota.Snapshot
	calls     []string
}

func (f *fakeQuotaChecker) Check(_ context.Context, tokens quota.Tokens, _ string) (quota.Snapshot, error) {
	f.calls = append(f.calls, tokens.AccessToken)
	snapshot, ok := f.snapshots[tokens.AccessToken]
	if !ok {
		return quota.Snapshot{}, fmt.Errorf("missing snapshot for %q", tokens.AccessToken)
	}
	return snapshot, nil
}

type fakeSwitcher struct {
	calls []string
	err   error
}

func (f *fakeSwitcher) Switch(name string) error {
	f.calls = append(f.calls, name)
	return f.err
}

type fakeNotifier struct {
	calls [][2]string
	err   error
}

func (f *fakeNotifier) Notify(title, body string) error {
	f.calls = append(f.calls, [2]string{title, body})
	return f.err
}

type sequencedQuotaChecker struct {
	responses map[string][]quota.Snapshot
	calls     []string
}

func (s *sequencedQuotaChecker) Check(_ context.Context, tokens quota.Tokens, _ string) (quota.Snapshot, error) {
	s.calls = append(s.calls, tokens.AccessToken)
	queue := s.responses[tokens.AccessToken]
	if len(queue) == 0 {
		return quota.Snapshot{}, fmt.Errorf("missing snapshot for %q", tokens.AccessToken)
	}
	snapshot := queue[0]
	if len(queue) > 1 {
		s.responses[tokens.AccessToken] = queue[1:]
	}
	return snapshot, nil
}

func rawProfile(t *testing.T, accessToken, accountID string) []byte {
	t.Helper()
	payload := map[string]any{
		"tokens": map[string]string{
			"access_token": accessToken,
			"account_id":   accountID,
		},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return raw
}

func TestRunOnceSwitchesToLowerWeeklyUsageCandidate(t *testing.T) {
	cfg := config.Default()
	cfg.Watch.PrimaryThresholdPercent = 80
	cfg.Watch.SecondaryThresholdPercent = 90

	loader := fakeProfileLoader{raw: map[string][]byte{
		"alpha": rawProfile(t, "alpha-token", "acct-alpha"),
		"beta":  rawProfile(t, "beta-token", "acct-beta"),
		"gamma": rawProfile(t, "gamma-token", "acct-gamma"),
	}}
	checker := &fakeQuotaChecker{snapshots: map[string]quota.Snapshot{
		"alpha-token": {PrimaryUsedPercent: 85, SecondaryUsedPercent: 70},
		"beta-token":  {PrimaryUsedPercent: 40, SecondaryUsedPercent: 20},
		"gamma-token": {PrimaryUsedPercent: 10, SecondaryUsedPercent: 30},
	}}
	switcher := &fakeSwitcher{}
	notifier := &fakeNotifier{}
	logger := &bytes.Buffer{}

	svc := Service{
		Profiles:    loader,
		QuotaClient: checker,
		Switcher:    switcher,
		Notifier:    notifier,
		Logger:      logger,
	}

	if err := svc.RunOnce(context.Background(), cfg, "alpha", []string{"alpha", "beta", "gamma"}); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}

	if got, want := switcher.calls, []string{"beta"}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("switch calls = %v, want %v", got, want)
	}
	if len(notifier.calls) != 1 {
		t.Fatalf("notifier calls = %v, want 1 call", notifier.calls)
	}
	if got := notifier.calls[0][1]; !strings.Contains(got, "alpha") || !strings.Contains(got, "beta") {
		t.Fatalf("notification body = %q, want source and destination profiles", got)
	}
	if got := logger.String(); !strings.Contains(got, "beta") {
		t.Fatalf("logger output = %q, want switch message", got)
	}
}

func TestRunOnceDoesNotSwitchToEqualOrWorseProfile(t *testing.T) {
	cfg := config.Default()
	cfg.Watch.PrimaryThresholdPercent = 80
	cfg.Watch.SecondaryThresholdPercent = 90

	loader := fakeProfileLoader{raw: map[string][]byte{
		"alpha": rawProfile(t, "alpha-token", "acct-alpha"),
		"beta":  rawProfile(t, "beta-token", "acct-beta"),
		"gamma": rawProfile(t, "gamma-token", "acct-gamma"),
	}}
	checker := &fakeQuotaChecker{snapshots: map[string]quota.Snapshot{
		"alpha-token": {PrimaryUsedPercent: 95, SecondaryUsedPercent: 40},
		"beta-token":  {PrimaryUsedPercent: 10, SecondaryUsedPercent: 40},
		"gamma-token": {PrimaryUsedPercent: 10, SecondaryUsedPercent: 55},
	}}
	switcher := &fakeSwitcher{}
	notifier := &fakeNotifier{}

	svc := Service{
		Profiles:    loader,
		QuotaClient: checker,
		Switcher:    switcher,
		Notifier:    notifier,
	}

	if err := svc.RunOnce(context.Background(), cfg, "alpha", []string{"alpha", "beta", "gamma"}); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}

	if len(switcher.calls) != 0 {
		t.Fatalf("switch calls = %v, want none", switcher.calls)
	}
	if len(notifier.calls) != 1 {
		t.Fatalf("notifier calls = %v, want depletion notice", notifier.calls)
	}
}

func TestRunOnceReportsAllAccountsDepletedWhenNoBetterCandidateExists(t *testing.T) {
	cfg := config.Default()
	cfg.Watch.PrimaryThresholdPercent = 80
	cfg.Watch.SecondaryThresholdPercent = 90

	loader := fakeProfileLoader{raw: map[string][]byte{
		"alpha": rawProfile(t, "alpha-token", "acct-alpha"),
		"beta":  rawProfile(t, "beta-token", "acct-beta"),
		"gamma": rawProfile(t, "gamma-token", "acct-gamma"),
	}}
	checker := &fakeQuotaChecker{snapshots: map[string]quota.Snapshot{
		"alpha-token": {PrimaryUsedPercent: 92, SecondaryUsedPercent: 61},
		"beta-token":  {PrimaryUsedPercent: 10, SecondaryUsedPercent: 61},
		"gamma-token": {PrimaryUsedPercent: 10, SecondaryUsedPercent: 74},
	}}
	switcher := &fakeSwitcher{}
	notifier := &fakeNotifier{}
	logger := &bytes.Buffer{}

	svc := Service{
		Profiles:    loader,
		QuotaClient: checker,
		Switcher:    switcher,
		Notifier:    notifier,
		Logger:      logger,
	}

	if err := svc.RunOnce(context.Background(), cfg, "alpha", []string{"alpha", "beta", "gamma"}); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}

	if len(switcher.calls) != 0 {
		t.Fatalf("switch calls = %v, want none", switcher.calls)
	}
	if len(notifier.calls) != 1 {
		t.Fatalf("notifier calls = %v, want depletion notice", notifier.calls)
	}
	if got := notifier.calls[0][1]; !strings.Contains(strings.ToLower(got), "depleted") {
		t.Fatalf("notification body = %q, want depleted message", got)
	}
	if got := logger.String(); !strings.Contains(strings.ToLower(got), "depleted") {
		t.Fatalf("logger output = %q, want depleted message", got)
	}
}

func TestRunPerformsStartupCalibrationOnce(t *testing.T) {
	cfg := config.Default()
	cfg.Watch.PrimaryThresholdPercent = 90
	cfg.Watch.SecondaryThresholdPercent = 95

	checker := &sequencedQuotaChecker{responses: map[string][]quota.Snapshot{
		"alpha-token": {{PrimaryUsedPercent: 15, SecondaryUsedPercent: 20}},
	}}
	events := make(chan TokenCountEvent)
	close(events)

	svc := Service{
		Profiles:      fakeProfileLoader{raw: map[string][]byte{"alpha": rawProfile(t, "alpha-token", "acct-alpha")}},
		QuotaClient:   checker,
		Switcher:      &fakeSwitcher{},
		Notifier:      &fakeNotifier{},
		TriggerEvents: events,
	}

	if err := svc.Run(context.Background(), cfg, "alpha", []string{"alpha"}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got, want := checker.calls, []string{"alpha-token"}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("quota calls = %v, want %v", got, want)
	}
}

func TestTriggeredEventConfirmsActiveBeforeCheckingCandidates(t *testing.T) {
	cfg := config.Default()
	cfg.Watch.PrimaryThresholdPercent = 90
	cfg.Watch.SecondaryThresholdPercent = 95

	checker := &sequencedQuotaChecker{responses: map[string][]quota.Snapshot{
		"alpha-token": {
			{PrimaryUsedPercent: 15, SecondaryUsedPercent: 20},
			{PrimaryUsedPercent: 93, SecondaryUsedPercent: 96},
		},
		"beta-token": {{PrimaryUsedPercent: 10, SecondaryUsedPercent: 30}},
	}}
	events := make(chan TokenCountEvent, 1)
	events <- TokenCountEvent{PrimaryUsedPercent: 91, SecondaryUsedPercent: 50}
	close(events)
	switcher := &fakeSwitcher{}

	svc := Service{
		Profiles: fakeProfileLoader{raw: map[string][]byte{
			"alpha": rawProfile(t, "alpha-token", "acct-alpha"),
			"beta":  rawProfile(t, "beta-token", "acct-beta"),
		}},
		QuotaClient:   checker,
		Switcher:      switcher,
		Notifier:      &fakeNotifier{},
		TriggerEvents: events,
	}

	if err := svc.Run(context.Background(), cfg, "alpha", []string{"alpha", "beta"}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got, want := checker.calls, []string{"alpha-token", "alpha-token", "beta-token"}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("quota calls = %v, want %v", got, want)
	}
	if got, want := switcher.calls, []string{"beta"}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("switch calls = %v, want %v", got, want)
	}
}

func TestTriggeredEventDoesNotProbeCandidatesWhenConfirmationFallsBelowThreshold(t *testing.T) {
	cfg := config.Default()
	cfg.Watch.PrimaryThresholdPercent = 90
	cfg.Watch.SecondaryThresholdPercent = 95

	checker := &sequencedQuotaChecker{responses: map[string][]quota.Snapshot{
		"alpha-token": {
			{PrimaryUsedPercent: 15, SecondaryUsedPercent: 20},
			{PrimaryUsedPercent: 60, SecondaryUsedPercent: 40},
		},
		"beta-token": {{PrimaryUsedPercent: 10, SecondaryUsedPercent: 30}},
	}}
	events := make(chan TokenCountEvent, 1)
	events <- TokenCountEvent{PrimaryUsedPercent: 92, SecondaryUsedPercent: 20}
	close(events)

	svc := Service{
		Profiles: fakeProfileLoader{raw: map[string][]byte{
			"alpha": rawProfile(t, "alpha-token", "acct-alpha"),
			"beta":  rawProfile(t, "beta-token", "acct-beta"),
		}},
		QuotaClient:   checker,
		Switcher:      &fakeSwitcher{},
		Notifier:      &fakeNotifier{},
		TriggerEvents: events,
	}

	if err := svc.Run(context.Background(), cfg, "alpha", []string{"alpha", "beta"}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got, want := checker.calls, []string{"alpha-token", "alpha-token"}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("quota calls = %v, want %v", got, want)
	}
}

func TestSwitchUpdatesActiveProfileAndEnforcesCooldown(t *testing.T) {
	cfg := config.Default()
	cfg.Watch.PrimaryThresholdPercent = 90
	cfg.Watch.SecondaryThresholdPercent = 95

	now := time.Date(2026, 3, 23, 9, 3, 31, 0, time.UTC)
	checker := &sequencedQuotaChecker{responses: map[string][]quota.Snapshot{
		"alpha-token": {
			{PrimaryUsedPercent: 15, SecondaryUsedPercent: 20},
			{PrimaryUsedPercent: 95, SecondaryUsedPercent: 97},
		},
		"beta-token": {{PrimaryUsedPercent: 10, SecondaryUsedPercent: 30}},
	}}
	events := make(chan TokenCountEvent, 2)
	events <- TokenCountEvent{PrimaryUsedPercent: 91, SecondaryUsedPercent: 40}
	events <- TokenCountEvent{PrimaryUsedPercent: 99, SecondaryUsedPercent: 99}
	close(events)
	switcher := &fakeSwitcher{}
	statePath := filepath.Join(t.TempDir(), "watch-state.toml")

	svc := Service{
		Profiles: fakeProfileLoader{raw: map[string][]byte{
			"alpha": rawProfile(t, "alpha-token", "acct-alpha"),
			"beta":  rawProfile(t, "beta-token", "acct-beta"),
		}},
		QuotaClient:   checker,
		Switcher:      switcher,
		Notifier:      &fakeNotifier{},
		TriggerEvents: events,
		StatePath:     statePath,
		Cooldown:      time.Minute,
		Now: func() time.Time {
			return now
		},
	}

	if err := svc.Run(context.Background(), cfg, "alpha", []string{"alpha", "beta"}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got, want := checker.calls, []string{"alpha-token", "alpha-token", "beta-token"}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("quota calls = %v, want %v", got, want)
	}
	if got, want := switcher.calls, []string{"beta"}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("switch calls = %v, want %v", got, want)
	}

	state, err := LoadState(statePath)
	if err != nil {
		t.Fatalf("LoadState() error = %v", err)
	}
	if state.Runtime.ActiveProfile != "beta" {
		t.Fatalf("Runtime.ActiveProfile = %q, want %q", state.Runtime.ActiveProfile, "beta")
	}
	if state.Runtime.CooldownUntil != now.Add(time.Minute) {
		t.Fatalf("Runtime.CooldownUntil = %v, want %v", state.Runtime.CooldownUntil, now.Add(time.Minute))
	}
}
