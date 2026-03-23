package watcher

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseTokenCountEventExtractsRateLimits(t *testing.T) {
	line := []byte(`{"timestamp":"2026-03-23T09:03:31Z","type":"event_msg","payload":{"type":"token_count","rate_limits":{"primary":{"used_percent":18},"secondary":{"used_percent":35},"plan_type":"team"}}}`)

	got, ok, err := ParseTokenCountEvent(line)
	if err != nil {
		t.Fatalf("ParseTokenCountEvent() error = %v", err)
	}
	if !ok {
		t.Fatal("ParseTokenCountEvent() ok = false, want true")
	}
	if got.PrimaryUsedPercent != 18 {
		t.Fatalf("PrimaryUsedPercent = %v, want 18", got.PrimaryUsedPercent)
	}
	if got.SecondaryUsedPercent != 35 {
		t.Fatalf("SecondaryUsedPercent = %v, want 35", got.SecondaryUsedPercent)
	}
	if got.PlanType != "team" {
		t.Fatalf("PlanType = %q, want %q", got.PlanType, "team")
	}
}

func TestParseTokenCountEventIgnoresMissingRateLimits(t *testing.T) {
	line := []byte(`{"timestamp":"2026-03-23T09:03:31Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"total_tokens":123}}}}`)

	_, ok, err := ParseTokenCountEvent(line)
	if err != nil {
		t.Fatalf("ParseTokenCountEvent() error = %v, want nil", err)
	}
	if ok {
		t.Fatal("ParseTokenCountEvent() ok = true, want false")
	}
}

func TestSessionReaderStreamsOnlyValidTokenCountEvents(t *testing.T) {
	root := t.TempDir()
	sessionDir := filepath.Join(root, "2026", "03", "23")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	path := filepath.Join(sessionDir, "rollout.jsonl")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	monitor := SessionMonitor{
		Root:         root,
		PollInterval: 10 * time.Millisecond,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	events, errs := monitor.Stream(ctx)
	time.Sleep(30 * time.Millisecond)

	lines := [][]byte{
		[]byte(`{"timestamp":"2026-03-23T09:03:00Z","type":"event_msg","payload":{"type":"user_message"}}`),
		[]byte(`not-json`),
		mustJSONLine(t, map[string]any{
			"timestamp": "2026-03-23T09:03:31Z",
			"type":      "event_msg",
			"payload": map[string]any{
				"type": "token_count",
				"rate_limits": map[string]any{
					"primary":   map[string]any{"used_percent": 91.0},
					"secondary": map[string]any{"used_percent": 35.0},
					"plan_type": "team",
				},
			},
		}),
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatalf("OpenFile() error = %v", err)
	}
	for _, line := range lines {
		if _, err := f.Write(append(line, '\n')); err != nil {
			t.Fatalf("Write() error = %v", err)
		}
	}
	if err := f.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	select {
	case err := <-errs:
		if err != nil {
			t.Fatalf("Stream() error = %v", err)
		}
	case event := <-events:
		if event.PrimaryUsedPercent != 91 {
			t.Fatalf("PrimaryUsedPercent = %v, want 91", event.PrimaryUsedPercent)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for token_count event")
	}

	select {
	case event := <-events:
		t.Fatalf("got unexpected extra event: %#v", event)
	case <-time.After(50 * time.Millisecond):
	}
}

func mustJSONLine(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return raw
}
