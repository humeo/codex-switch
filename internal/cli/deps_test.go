package cli

import (
	"path/filepath"
	"testing"
)

func TestDefaultDependenciesUsesHomeDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	deps, err := DefaultDependencies()
	if err != nil {
		t.Fatalf("DefaultDependencies() error = %v", err)
	}

	if deps.ConfigPath != filepath.Join(home, ".codex-switch", "config.toml") {
		t.Fatalf("ConfigPath = %q", deps.ConfigPath)
	}
	if deps.ProfilesDir != filepath.Join(home, ".codex-switch", "profiles") {
		t.Fatalf("ProfilesDir = %q", deps.ProfilesDir)
	}
	if deps.AuthPath != filepath.Join(home, ".codex", "auth.json") {
		t.Fatalf("AuthPath = %q", deps.AuthPath)
	}
	if deps.Stdout == nil {
		t.Fatal("Stdout = nil")
	}
	if deps.Stderr == nil {
		t.Fatal("Stderr = nil")
	}
}
