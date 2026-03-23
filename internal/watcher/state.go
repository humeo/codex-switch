package watcher

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	toml "github.com/pelletier/go-toml/v2"
)

const maxSnapshotSamples = 5

type WatchState struct {
	LastCleanupAt time.Time               `toml:"last_cleanup_at"`
	Runtime       RuntimeState            `toml:"runtime"`
	Profiles      map[string]ProfileState `toml:"profiles"`
}

type RuntimeState struct {
	ActiveProfile string    `toml:"active_profile"`
	CooldownUntil time.Time `toml:"cooldown_until"`
}

type ProfileState struct {
	LastConfirmedAt          time.Time        `toml:"last_confirmed_at"`
	LastTriggeredAt          time.Time        `toml:"last_triggered_at"`
	LastTriggerSource        string           `toml:"last_trigger_source"`
	LastPlan                 string           `toml:"last_plan"`
	LastPrimaryUsedPercent   int              `toml:"last_primary_used_percent"`
	LastSecondaryUsedPercent int              `toml:"last_secondary_used_percent"`
	LastPrimaryResetAt       time.Time        `toml:"last_primary_reset_at"`
	LastSecondaryResetAt     time.Time        `toml:"last_secondary_reset_at"`
	Samples                  []SnapshotSample `toml:"samples"`
}

type SnapshotSample struct {
	At                   time.Time `toml:"at"`
	PrimaryUsedPercent   int       `toml:"primary_used_percent"`
	SecondaryUsedPercent int       `toml:"secondary_used_percent"`
}

type CheckEvent struct {
	At                   time.Time `json:"at"`
	Profile              string    `json:"profile"`
	Kind                 string    `json:"kind"`
	Trigger              string    `json:"trigger,omitempty"`
	Success              bool      `json:"success"`
	PlanType             string    `json:"plan_type,omitempty"`
	PrimaryUsedPercent   int       `json:"primary_used_percent,omitempty"`
	SecondaryUsedPercent int       `json:"secondary_used_percent,omitempty"`
	EstimatedTokens      int       `json:"estimated_tokens,omitempty"`
	EstimatedCostUSD     string    `json:"estimated_cost_usd,omitempty"`
	SwitchedTo           string    `json:"switched_to,omitempty"`
}

func LoadState(path string) (WatchState, error) {
	state := WatchState{
		Profiles: map[string]ProfileState{},
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return state, nil
		}
		return WatchState{}, err
	}
	if len(data) == 0 {
		return state, nil
	}
	if err := toml.Unmarshal(data, &state); err != nil {
		return WatchState{}, err
	}
	if state.Profiles == nil {
		state.Profiles = map[string]ProfileState{}
	}
	for name, profile := range state.Profiles {
		if len(profile.Samples) > maxSnapshotSamples {
			profile.Samples = profile.Samples[len(profile.Samples)-maxSnapshotSamples:]
			state.Profiles[name] = profile
		}
	}

	return state, nil
}

func SaveState(path string, state WatchState) error {
	if state.Profiles == nil {
		state.Profiles = map[string]ProfileState{}
	}
	for name, profile := range state.Profiles {
		if len(profile.Samples) > maxSnapshotSamples {
			profile.Samples = profile.Samples[len(profile.Samples)-maxSnapshotSamples:]
			state.Profiles[name] = profile
		}
	}

	data, err := toml.Marshal(state)
	if err != nil {
		return err
	}
	return writeFileAtomically(path, data, 0o600)
}

func AppendCheckEvent(path string, event CheckEvent) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	raw, err := json.Marshal(event)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(raw, '\n'))
	return err
}

func AppendLogLine(path string, at time.Time, msg string) error {
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.WriteString(at.UTC().Format(time.RFC3339Nano) + " " + strings.TrimSpace(msg) + "\n"); err != nil {
		return err
	}
	return nil
}

func PruneJSONLFile(path string, cutoff time.Time) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}

	var kept [][]byte
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}

		var event CheckEvent
		if err := json.Unmarshal(line, &event); err != nil {
			continue
		}
		if event.At.Before(cutoff) {
			continue
		}

		copyLine := append([]byte(nil), line...)
		kept = append(kept, copyLine)
	}
	if err := scanner.Err(); err != nil {
		return err
	}

	return writeFileAtomically(path, bytes.Join(append(kept, []byte{}), []byte("\n")), 0o600)
}

func PruneLogFile(path string, cutoff time.Time) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}

	var kept []string
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		parts := strings.SplitN(trimmed, " ", 2)
		ts, err := time.Parse(time.RFC3339Nano, parts[0])
		if err != nil {
			continue
		}
		if ts.Before(cutoff) {
			continue
		}
		kept = append(kept, trimmed)
	}
	if err := scanner.Err(); err != nil {
		return err
	}

	return writeFileAtomically(path, []byte(strings.Join(kept, "\n")), 0o600)
}

func writeFileAtomically(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "watch-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() {
		_ = os.Remove(tmpName)
	}

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return err
	}
	return nil
}
