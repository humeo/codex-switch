package config

import (
	"errors"
	"os"
	"path/filepath"
	"time"

	toml "github.com/pelletier/go-toml/v2"
)

type Config struct {
	CheckModel    string                `toml:"check_model"`
	ActiveProfile string                `toml:"active_profile"`
	AutoCheck     bool                  `toml:"auto_check"`
	Watch         WatchConfig           `toml:"watch"`
	Cache         map[string]QuotaCache `toml:"cache"`
}

type WatchConfig struct {
	IntervalMinutes           int  `toml:"interval_minutes"`
	PrimaryThresholdPercent   int  `toml:"primary_threshold_percent"`
	SecondaryThresholdPercent int  `toml:"secondary_threshold_percent"`
	Notify                    bool `toml:"notify"`
}

type QuotaCache struct {
	Plan                       string    `toml:"plan"`
	PrimaryUsedPercent         int       `toml:"primary_used_percent"`
	SecondaryUsedPercent       int       `toml:"secondary_used_percent"`
	PrimaryResetAfterSeconds   int64     `toml:"primary_reset_after_seconds"`
	SecondaryResetAfterSeconds int64     `toml:"secondary_reset_after_seconds"`
	PrimaryResetAt             time.Time `toml:"primary_reset_at"`
	SecondaryResetAt           time.Time `toml:"secondary_reset_at"`
	HasCredits                 bool      `toml:"has_credits"`
	CreditsBalance             string    `toml:"credits_balance"`
	CheckedAt                  time.Time `toml:"checked_at"`
}

func Default() Config {
	return Config{
		CheckModel:    "gpt-5.4-mini",
		ActiveProfile: "",
		AutoCheck:     true,
		Watch: WatchConfig{
			IntervalMinutes:           5,
			PrimaryThresholdPercent:   90,
			SecondaryThresholdPercent: 95,
			Notify:                    true,
		},
		Cache: map[string]QuotaCache{},
	}
}

func Load(path string) (Config, error) {
	cfg := Default()

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cfg, nil
		}
		return Config{}, err
	}

	if len(data) == 0 {
		return cfg, nil
	}

	if err := toml.Unmarshal(data, &cfg); err != nil {
		return Config{}, err
	}
	if cfg.Cache == nil {
		cfg.Cache = map[string]QuotaCache{}
	}

	return cfg, nil
}

func Save(path string, cfg Config) error {
	if cfg.Cache == nil {
		cfg.Cache = map[string]QuotaCache{}
	}

	data, err := toml.Marshal(cfg)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	return os.WriteFile(path, data, 0o600)
}
