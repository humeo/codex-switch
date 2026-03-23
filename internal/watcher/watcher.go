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
	Profiles    ProfileLoader
	QuotaClient QuotaChecker
	Switcher    ProfileSwitcher
	Notifier    Notifier
	Logger      io.Writer
}

type profileStatus struct {
	name     string
	snapshot quota.Snapshot
}

func (s Service) RunOnce(ctx context.Context, cfg config.Config, active string, names []string) error {
	if active == "" {
		return nil
	}
	if s.Profiles == nil {
		return errors.New("watcher: profile loader is required")
	}
	if s.QuotaClient == nil {
		return errors.New("watcher: quota client is required")
	}

	activeSnapshot, err := s.snapshotForName(ctx, cfg, active)
	if err != nil {
		return err
	}
	if !thresholdReached(activeSnapshot, cfg) {
		return nil
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
			return err
		}
		candidates = append(candidates, profileStatus{name: name, snapshot: snapshot})
	}

	if len(candidates) == 0 {
		s.reportDepleted(active)
		return nil
	}

	best := chooseBestCandidate(candidates)
	if best.snapshot.SecondaryUsedPercent >= activeSnapshot.SecondaryUsedPercent {
		s.reportDepleted(active)
		return nil
	}

	if s.Switcher == nil {
		return errors.New("watcher: switcher is required")
	}
	if err := s.Switcher.Switch(best.name); err != nil {
		return err
	}

	s.reportSwitch(active, best.name)
	return nil
}

func (s Service) Run(ctx context.Context, cfg config.Config, active string, names []string) error {
	if err := s.RunOnce(ctx, cfg, active, names); err != nil {
		return err
	}

	interval := time.Duration(cfg.Watch.IntervalMinutes) * time.Minute
	if interval <= 0 {
		interval = 5 * time.Minute
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := s.RunOnce(ctx, cfg, active, names); err != nil {
				return err
			}
		}
	}
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
	if s.Logger == nil {
		return
	}
	if !strings.HasSuffix(msg, "\n") {
		msg += "\n"
	}
	_, _ = io.WriteString(s.Logger, msg)
}

func (s Service) notify(title, body string) {
	if s.Notifier == nil {
		return
	}
	_ = s.Notifier.Notify(title, body)
}
