package config

import (
	"path/filepath"
	"testing"
)

func TestLoadCreatesDefaultsWhenMissing(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "config.toml"))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if !cfg.AutoCheck {
		t.Fatalf("AutoCheck default = false, want true")
	}
	if cfg.CheckModel == "" {
		t.Fatal("CheckModel should have a default")
	}
}

func TestSavePersistsCachedQuotaByProfile(t *testing.T) {
	cfg := Default()
	cfg.Cache["work"] = QuotaCache{Plan: "plus", PrimaryUsedPercent: 12}

	path := filepath.Join(t.TempDir(), "config.toml")
	if err := Save(path, cfg); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.Cache["work"].PrimaryUsedPercent != 12 {
		t.Fatalf("cache not persisted")
	}
}
