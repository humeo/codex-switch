package watcher

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"codex-switch/internal/config"
)

const defaultSessionPollInterval = time.Second

type TokenCountEvent struct {
	Timestamp            time.Time
	PrimaryUsedPercent   float64
	SecondaryUsedPercent float64
	PlanType             string
}

func (e TokenCountEvent) Exceeds(cfg config.Config) bool {
	return e.PrimaryUsedPercent >= float64(cfg.Watch.PrimaryThresholdPercent) ||
		e.SecondaryUsedPercent >= float64(cfg.Watch.SecondaryThresholdPercent)
}

type SessionMonitor struct {
	Root         string
	PollInterval time.Duration
}

func ParseTokenCountEvent(line []byte) (TokenCountEvent, bool, error) {
	var envelope struct {
		Timestamp time.Time `json:"timestamp"`
		Type      string    `json:"type"`
		Payload   *struct {
			Type       string `json:"type"`
			RateLimits *struct {
				Primary *struct {
					UsedPercent float64 `json:"used_percent"`
				} `json:"primary"`
				Secondary *struct {
					UsedPercent float64 `json:"used_percent"`
				} `json:"secondary"`
				PlanType string `json:"plan_type"`
			} `json:"rate_limits"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(line, &envelope); err != nil {
		return TokenCountEvent{}, false, err
	}
	if envelope.Type != "event_msg" || envelope.Payload == nil || envelope.Payload.Type != "token_count" {
		return TokenCountEvent{}, false, nil
	}
	if envelope.Payload.RateLimits == nil || envelope.Payload.RateLimits.Primary == nil || envelope.Payload.RateLimits.Secondary == nil {
		return TokenCountEvent{}, false, nil
	}

	return TokenCountEvent{
		Timestamp:            envelope.Timestamp,
		PrimaryUsedPercent:   envelope.Payload.RateLimits.Primary.UsedPercent,
		SecondaryUsedPercent: envelope.Payload.RateLimits.Secondary.UsedPercent,
		PlanType:             envelope.Payload.RateLimits.PlanType,
	}, true, nil
}

func (m SessionMonitor) Stream(ctx context.Context) (<-chan TokenCountEvent, <-chan error) {
	out := make(chan TokenCountEvent)
	errs := make(chan error, 1)

	go func() {
		defer close(out)
		defer close(errs)

		interval := m.PollInterval
		if interval <= 0 {
			interval = defaultSessionPollInterval
		}

		offsets := map[string]int64{}
		initialScan := true
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			if err := scanSessionFiles(m.Root, offsets, initialScan, out); err != nil {
				select {
				case errs <- err:
				default:
				}
			}
			initialScan = false

			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()

	return out, errs
}

func scanSessionFiles(root string, offsets map[string]int64, initialScan bool, out chan<- TokenCountEvent) error {
	if root == "" {
		return nil
	}

	seen := map[string]struct{}{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Ext(path) != ".jsonl" {
			return nil
		}
		seen[path] = struct{}{}

		info, err := d.Info()
		if err != nil {
			return err
		}

		offset, ok := offsets[path]
		if !ok {
			if initialScan {
				offsets[path] = info.Size()
				return nil
			}
			offset = 0
		}
		if info.Size() < offset {
			offset = 0
		}
		if info.Size() == offset {
			offsets[path] = offset
			return nil
		}

		nextOffset, events, err := readSessionEvents(path, offset)
		if err != nil {
			return err
		}
		offsets[path] = nextOffset
		for _, event := range events {
			out <- event
		}
		return nil
	})
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	for path := range offsets {
		if _, ok := seen[path]; !ok {
			delete(offsets, path)
		}
	}

	return nil
}

func readSessionEvents(path string, offset int64) (int64, []TokenCountEvent, error) {
	f, err := os.Open(path)
	if err != nil {
		return offset, nil, err
	}
	defer f.Close()

	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return offset, nil, err
	}

	reader := bufio.NewReader(f)
	current := offset
	var events []TokenCountEvent

	for {
		line, err := reader.ReadBytes('\n')
		current += int64(len(line))
		if len(line) > 0 {
			if event, ok, parseErr := ParseTokenCountEvent(line); parseErr == nil && ok {
				events = append(events, event)
			}
		}
		if err != nil {
			if err == io.EOF {
				return current, events, nil
			}
			return current, nil, err
		}
	}
}
