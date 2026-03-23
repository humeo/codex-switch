package watcher

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadStateMissingFileReturnsDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "watch-state.toml")

	got, err := LoadState(path)
	if err != nil {
		t.Fatalf("LoadState() error = %v", err)
	}
	if got.Profiles == nil {
		t.Fatal("LoadState() Profiles = nil, want initialized map")
	}
	if !got.LastCleanupAt.IsZero() {
		t.Fatalf("LoadState() LastCleanupAt = %v, want zero", got.LastCleanupAt)
	}
}

func TestSaveStateRoundTripsSamplesAndRuntime(t *testing.T) {
	path := filepath.Join(t.TempDir(), "watch-state.toml")
	now := time.Date(2026, 3, 23, 9, 3, 31, 0, time.UTC)

	want := WatchState{
		LastCleanupAt: now,
		Runtime: RuntimeState{
			ActiveProfile: "personal",
			CooldownUntil: now.Add(time.Minute),
		},
		Profiles: map[string]ProfileState{
			"personal": {
				LastConfirmedAt:          now,
				LastTriggeredAt:          now.Add(-time.Second),
				LastTriggerSource:        "session_rate_limits",
				LastPlan:                 "plus",
				LastPrimaryUsedPercent:   13,
				LastSecondaryUsedPercent: 90,
				LastPrimaryResetAt:       now.Add(4 * time.Hour),
				LastSecondaryResetAt:     now.Add(48 * time.Hour),
				Samples: []SnapshotSample{{
					At:                   now,
					PrimaryUsedPercent:   13,
					SecondaryUsedPercent: 90,
				}},
			},
		},
	}

	if err := SaveState(path, want); err != nil {
		t.Fatalf("SaveState() error = %v", err)
	}

	got, err := LoadState(path)
	if err != nil {
		t.Fatalf("LoadState() error = %v", err)
	}
	if got.Runtime.ActiveProfile != want.Runtime.ActiveProfile {
		t.Fatalf("Runtime.ActiveProfile = %q, want %q", got.Runtime.ActiveProfile, want.Runtime.ActiveProfile)
	}
	if got.Runtime.CooldownUntil != want.Runtime.CooldownUntil {
		t.Fatalf("Runtime.CooldownUntil = %v, want %v", got.Runtime.CooldownUntil, want.Runtime.CooldownUntil)
	}

	profile := got.Profiles["personal"]
	if profile.LastPlan != "plus" {
		t.Fatalf("LastPlan = %q, want %q", profile.LastPlan, "plus")
	}
	if len(profile.Samples) != 1 {
		t.Fatalf("Samples len = %d, want 1", len(profile.Samples))
	}
	if profile.Samples[0].SecondaryUsedPercent != 90 {
		t.Fatalf("Samples[0].SecondaryUsedPercent = %d, want 90", profile.Samples[0].SecondaryUsedPercent)
	}
}

func TestPruneJSONLFileDropsEntriesOlderThanRetention(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "watch-checks.jsonl")
	now := time.Date(2026, 3, 23, 12, 0, 0, 0, time.UTC)

	entries := []CheckEvent{
		{At: now.Add(-8 * 24 * time.Hour), Profile: "old", Kind: "active_check"},
		{At: now.Add(-6 * 24 * time.Hour), Profile: "fresh", Kind: "active_check"},
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatalf("OpenFile() error = %v", err)
	}
	defer f.Close()
	for _, entry := range entries {
		raw, err := json.Marshal(entry)
		if err != nil {
			t.Fatalf("json.Marshal() error = %v", err)
		}
		if _, err := f.Write(append(raw, '\n')); err != nil {
			t.Fatalf("Write() error = %v", err)
		}
	}

	if err := PruneJSONLFile(path, now.Add(-7*24*time.Hour)); err != nil {
		t.Fatalf("PruneJSONLFile() error = %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	text := strings.TrimSpace(string(data))
	if strings.Contains(text, `"profile":"old"`) {
		t.Fatalf("pruned file still contains old entry: %s", text)
	}
	if !strings.Contains(text, `"profile":"fresh"`) {
		t.Fatalf("pruned file missing fresh entry: %s", text)
	}
}
