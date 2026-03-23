package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex-switch/internal/config"
	"codex-switch/internal/profile"
)

func TestRootCommandIncludesCoreSubcommands(t *testing.T) {
	cmd := NewRootCommand(Dependencies{})
	names := map[string]bool{}
	for _, child := range cmd.Commands() {
		names[child.Name()] = true
	}

	for _, want := range []string{"auth", "list", "use", "status", "watch", "remove"} {
		if !names[want] {
			t.Fatalf("missing subcommand %q", want)
		}
	}
}

func TestUseCommand(t *testing.T) {
	dir := t.TempDir()
	profilesDir := filepath.Join(dir, "profiles")
	authPath := filepath.Join(dir, "auth.json")
	cfgPath := filepath.Join(dir, "config.toml")

	store := profile.NewStore(profilesDir)
	raw := []byte(`{"auth_mode":"chatgpt","tokens":{"access_token":"abc"}}`)
	if err := store.Save("work", raw); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if err := config.Save(cfgPath, config.Default()); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd := NewRootCommand(Dependencies{
		ConfigPath:  cfgPath,
		ProfilesDir: profilesDir,
		AuthPath:    authPath,
		Stdout:      stdout,
		Stderr:      stderr,
	})
	cmd.SetArgs([]string{"use", "work"})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	gotAuth, err := os.ReadFile(authPath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(gotAuth) != string(raw) {
		t.Fatalf("auth.json = %s, want %s", gotAuth, raw)
	}

	gotCfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if gotCfg.ActiveProfile != "work" {
		t.Fatalf("active_profile = %q, want work", gotCfg.ActiveProfile)
	}
	if got := stdout.String(); !strings.Contains(got, "active profile: work") {
		t.Fatalf("stdout = %q, want confirmation line", got)
	}
}

func TestRemoveCommand(t *testing.T) {
	dir := t.TempDir()
	profilesDir := filepath.Join(dir, "profiles")
	authPath := filepath.Join(dir, "auth.json")
	cfgPath := filepath.Join(dir, "config.toml")

	store := profile.NewStore(profilesDir)
	if err := store.Save("active", []byte(`{"tokens":{"access_token":"abc"}}`)); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	cfg := config.Default()
	cfg.ActiveProfile = "active"
	if err := config.Save(cfgPath, cfg); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if err := os.WriteFile(authPath, []byte(`{"tokens":{"access_token":"abc"}}`), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	t.Run("rejects active without force", func(t *testing.T) {
		stdout := &bytes.Buffer{}
		stderr := &bytes.Buffer{}
		cmd := NewRootCommand(Dependencies{
			ConfigPath:  cfgPath,
			ProfilesDir: profilesDir,
			AuthPath:    authPath,
			Stdout:      stdout,
			Stderr:      stderr,
		})
		cmd.SetArgs([]string{"remove", "active"})

		err := cmd.Execute()
		if err == nil {
			t.Fatal("Execute() error = nil, want non-nil")
		}

		if _, statErr := os.Stat(filepath.Join(profilesDir, "active.json")); statErr != nil {
			t.Fatalf("profile should still exist: %v", statErr)
		}
		gotCfg, err := config.Load(cfgPath)
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}
		if gotCfg.ActiveProfile != "active" {
			t.Fatalf("active_profile = %q, want active", gotCfg.ActiveProfile)
		}
		if got := stderr.String(); !strings.Contains(got, `use --force to remove it`) {
			t.Fatalf("stderr = %q, want active-profile rejection", got)
		}
	})

	t.Run("force removes active and clears config", func(t *testing.T) {
		stdout := &bytes.Buffer{}
		stderr := &bytes.Buffer{}
		cmd := NewRootCommand(Dependencies{
			ConfigPath:  cfgPath,
			ProfilesDir: profilesDir,
			AuthPath:    authPath,
			Stdout:      stdout,
			Stderr:      stderr,
		})
		cmd.SetArgs([]string{"remove", "active", "--force"})

		err := cmd.Execute()
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}

		if _, statErr := os.Stat(filepath.Join(profilesDir, "active.json")); !os.IsNotExist(statErr) {
			t.Fatalf("profile unexpectedly exists or stat failed: %v", statErr)
		}
		gotCfg, err := config.Load(cfgPath)
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}
		if gotCfg.ActiveProfile != "" {
			t.Fatalf("active_profile = %q, want empty", gotCfg.ActiveProfile)
		}
		if got := stdout.String(); !strings.Contains(got, "removed profile: active") {
			t.Fatalf("stdout = %q, want confirmation line", got)
		}
	})
}
