package watcher

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

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
