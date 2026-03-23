package cli

import (
	"context"

	"codex-switch/internal/config"
	"codex-switch/internal/profile"
)

type quotaSource string

const (
	quotaSourceLive  quotaSource = "live"
	quotaSourceCache quotaSource = "cache"
)

func loadProfileRows(ctx context.Context, deps Dependencies, cfg *config.Config, store profile.Store, names []string, checkLive bool, allowCacheFallback bool) ([]listRow, error) {
	var checker quotaChecker
	if checkLive {
		checker = quotaCheckerFactory()
	}

	rows := make([]listRow, 0, len(names))
	cacheDirty := false
	for _, name := range names {
		raw, err := store.Load(name)
		if err != nil {
			return nil, err
		}

		row := listRow{
			name:     name,
			snapshot: snapshotFromCache(cfg.Cache[name]),
			active:   name == cfg.ActiveProfile,
			source:   quotaSourceCache,
		}

		if checkLive {
			tokens, err := tokensFromProfile(raw)
			if err != nil {
				return nil, err
			}
			liveSnapshot, err := checker.Check(ctx, tokens, cfg.CheckModel)
			if err != nil {
				if !allowCacheFallback {
					return nil, err
				}
			} else {
				row.snapshot = liveSnapshot
				row.source = quotaSourceLive
				if cfg.Cache == nil {
					cfg.Cache = map[string]config.QuotaCache{}
				}
				cfg.Cache[name] = cacheFromSnapshot(liveSnapshot)
				cacheDirty = true
			}
		}

		rows = append(rows, row)
	}

	if cacheDirty {
		if err := config.Save(deps.ConfigPath, *cfg); err != nil {
			return nil, err
		}
	}

	sortListRows(rows)
	return rows, nil
}

func remainingPercent(used int) int {
	remaining := 100 - used
	if remaining < 0 {
		return 0
	}
	if remaining > 100 {
		return 100
	}
	return remaining
}
