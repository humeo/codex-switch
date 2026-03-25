package cli

import (
	"context"
	"time"

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
	now := timeNow()
	for _, name := range names {
		row, updated, err := loadProfileRow(ctx, cfg, store, name, checkLive, allowCacheFallback, checker, now)
		if err != nil {
			return nil, err
		}
		rows = append(rows, row)
		cacheDirty = cacheDirty || updated
	}

	if cacheDirty {
		if err := config.Save(deps.ConfigPath, *cfg); err != nil {
			return nil, err
		}
	}

	sortListRows(rows)
	return rows, nil
}

func loadProfileRow(ctx context.Context, cfg *config.Config, store profile.Store, name string, checkLive bool, allowCacheFallback bool, checker quotaChecker, now time.Time) (listRow, bool, error) {
	cache := cfg.Cache[name]
	row := listRow{
		name:     name,
		snapshot: snapshotFromCache(cache),
		active:   name == cfg.ActiveProfile,
		source:   quotaSourceCache,
	}

	if !checkLive || cacheIsFresh(cache, now) {
		return row, false, nil
	}

	raw, err := store.Load(name)
	if err != nil {
		return listRow{}, false, err
	}

	tokens, err := tokensFromProfile(raw)
	if err != nil {
		return listRow{}, false, err
	}

	liveSnapshot, err := checker.Check(ctx, tokens, cfg.CheckModel)
	if err != nil {
		if !allowCacheFallback {
			return listRow{}, false, err
		}
		return row, false, nil
	}

	liveSnapshot = mergeRateLimitedMetadata(liveSnapshot, row.snapshot)
	row.snapshot = liveSnapshot
	row.source = quotaSourceLive
	if cfg.Cache == nil {
		cfg.Cache = map[string]config.QuotaCache{}
	}
	cfg.Cache[name] = cacheFromSnapshot(liveSnapshot, now)
	return row, true, nil
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
