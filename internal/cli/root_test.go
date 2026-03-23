package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex-switch/internal/config"
	"codex-switch/internal/profile"
	"codex-switch/internal/quota"
)

type fakeAuthRunner struct {
	t         *testing.T
	authPath  string
	loginData []byte
	calls     [][]string
}

func (r *fakeAuthRunner) Run(_ context.Context, name string, args ...string) error {
	r.calls = append(r.calls, append([]string{name}, args...))
	if name != "codex" {
		r.t.Fatalf("runner name = %q, want codex", name)
	}
	if len(args) == 1 && args[0] == "login" {
		if err := os.WriteFile(r.authPath, r.loginData, 0o600); err != nil {
			r.t.Fatalf("WriteFile() error = %v", err)
		}
	}
	return nil
}

func useAuthRunner(t *testing.T, runner Runner) {
	t.Helper()
	orig := authRunnerFactory
	authRunnerFactory = func() Runner { return runner }
	t.Cleanup(func() { authRunnerFactory = orig })
}

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

func TestListCommandShowsEmptyState(t *testing.T) {
	dir := t.TempDir()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	cmd := NewRootCommand(Dependencies{
		ConfigPath:  filepath.Join(dir, "config.toml"),
		ProfilesDir: filepath.Join(dir, "profiles"),
		Stdout:      stdout,
		Stderr:      stderr,
	})
	cmd.SetArgs([]string{"list"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	got := strings.TrimSpace(stdout.String())
	want := "No profiles found. Run 'codex-switch auth <name>' to add one."
	if got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestListCommandUsesLiveQuotaByDefault(t *testing.T) {
	dir := t.TempDir()
	profilesDir := filepath.Join(dir, "profiles")
	cfgPath := filepath.Join(dir, "config.toml")

	store := profile.NewStore(profilesDir)
	writeProfile(t, store, "alpha", []byte(`{"tokens":{"access_token":"alpha-token","account_id":"acct-alpha"}}`))
	writeProfile(t, store, "beta", []byte(`{"tokens":{"access_token":"beta-token","account_id":"acct-beta"}}`))
	cfg := config.Default()
	cfg.ActiveProfile = "beta"
	if err := config.Save(cfgPath, cfg); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	calls := []string{}
	useQuotaChecker(t, func(_ context.Context, tokens quota.Tokens, model string) (quota.Snapshot, error) {
		calls = append(calls, tokens.AccessToken+"|"+model)
		switch tokens.AccessToken {
		case "alpha-token":
			return quota.Snapshot{
				Plan:                 "plus",
				PrimaryUsedPercent:   2,
				SecondaryUsedPercent: 30,
				PrimaryResetAfter:    2 * time.Hour,
				SecondaryResetAfter:  48 * time.Hour,
			}, nil
		case "beta-token":
			return quota.Snapshot{
				Plan:                 "plus",
				PrimaryUsedPercent:   1,
				SecondaryUsedPercent: 12,
				PrimaryResetAfter:    90 * time.Minute,
				SecondaryResetAfter:  24 * time.Hour,
			}, nil
		default:
			t.Fatalf("unexpected access token %q", tokens.AccessToken)
			return quota.Snapshot{}, nil
		}
	})

	stdout := &bytes.Buffer{}
	cmd := NewRootCommand(Dependencies{
		ConfigPath:  cfgPath,
		ProfilesDir: profilesDir,
		Stdout:      stdout,
	})
	cmd.SetArgs([]string{"list"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	got := stdout.String()
	if !strings.Contains(got, "NAME") || !strings.Contains(got, "WEEKLY USED") {
		t.Fatalf("stdout = %q, want list header", got)
	}
	betaLine := lineContaining(got, "beta")
	alphaLine := lineContaining(got, "alpha")
	if betaLine == "" || alphaLine == "" {
		t.Fatalf("stdout = %q, want beta and alpha rows", got)
	}
	if strings.Index(got, betaLine) > strings.Index(got, alphaLine) {
		t.Fatalf("stdout = %q, want beta before alpha", got)
	}
	if !strings.Contains(betaLine, "*") {
		t.Fatalf("beta row = %q, want active marker", betaLine)
	}
	if len(calls) != 2 {
		t.Fatalf("quota calls = %#v, want two live checks", calls)
	}

	gotCfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if gotCfg.Cache["beta"].SecondaryUsedPercent != 12 {
		t.Fatalf("cache = %+v, want live snapshot persisted", gotCfg.Cache["beta"])
	}
}

func TestListCommandNoCheckUsesCachedSnapshot(t *testing.T) {
	dir := t.TempDir()
	profilesDir := filepath.Join(dir, "profiles")
	cfgPath := filepath.Join(dir, "config.toml")

	store := profile.NewStore(profilesDir)
	writeProfile(t, store, "cached", []byte(`{"tokens":{"access_token":"cached-token","account_id":"acct-cached"}}`))
	cfg := config.Default()
	cfg.Cache["cached"] = config.QuotaCache{
		Plan:                       "plus",
		PrimaryUsedPercent:         7,
		SecondaryUsedPercent:       14,
		PrimaryResetAfterSeconds:   int64((3 * time.Hour).Seconds()),
		SecondaryResetAfterSeconds: int64((6 * time.Hour).Seconds()),
	}
	if err := config.Save(cfgPath, cfg); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	called := false
	useQuotaChecker(t, func(_ context.Context, _ quota.Tokens, _ string) (quota.Snapshot, error) {
		called = true
		return quota.Snapshot{}, nil
	})

	stdout := &bytes.Buffer{}
	cmd := NewRootCommand(Dependencies{
		ConfigPath:  cfgPath,
		ProfilesDir: profilesDir,
		Stdout:      stdout,
	})
	cmd.SetArgs([]string{"list", "--no-check"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if called {
		t.Fatal("quota checker called, want cached-only output")
	}
	got := stdout.String()
	for _, want := range []string{"cached", "plus", "7%", "14%"} {
		if !strings.Contains(got, want) {
			t.Fatalf("stdout = %q, want %q", got, want)
		}
	}
}

func TestStatusCommandShowsActiveProfileDetails(t *testing.T) {
	dir := t.TempDir()
	profilesDir := filepath.Join(dir, "profiles")
	cfgPath := filepath.Join(dir, "config.toml")

	store := profile.NewStore(profilesDir)
	writeProfile(t, store, "work", []byte(`{"tokens":{"access_token":"work-token","account_id":"acct-work"}}`))
	cfg := config.Default()
	cfg.ActiveProfile = "work"
	cfg.Cache["work"] = config.QuotaCache{
		Plan:                       "plus",
		PrimaryUsedPercent:         2,
		SecondaryUsedPercent:       86,
		PrimaryResetAfterSeconds:   int64((4*time.Hour + 20*time.Minute).Seconds()),
		SecondaryResetAfterSeconds: int64((2*24*time.Hour + 9*time.Hour).Seconds()),
		PrimaryResetAt:             time.Date(2026, 3, 23, 14, 31, 0, 0, time.UTC),
		SecondaryResetAt:           time.Date(2026, 3, 25, 18, 42, 0, 0, time.UTC),
	}
	if err := config.Save(cfgPath, cfg); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	called := false
	useQuotaChecker(t, func(_ context.Context, _ quota.Tokens, _ string) (quota.Snapshot, error) {
		called = true
		return quota.Snapshot{}, nil
	})

	stdout := &bytes.Buffer{}
	cmd := NewRootCommand(Dependencies{
		ConfigPath:  cfgPath,
		ProfilesDir: profilesDir,
		Stdout:      stdout,
	})
	cmd.SetArgs([]string{"status", "--no-check"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if called {
		t.Fatal("quota checker called, want cached-only output")
	}
	got := stdout.String()
	for _, want := range []string{
		"Active: work (plus)",
		"Used: 2%",
		"Used: 86%",
		"Resets in: 4h 20m (at 2026-03-23 14:31)",
		"Resets in: 2d 9h (at 2026-03-25 18:42)",
		"Credits: none",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("stdout = %q, want %q", got, want)
		}
	}
}

func writeProfile(t *testing.T, store profile.Store, name string, raw []byte) {
	t.Helper()
	if err := store.Save(name, raw); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
}

func useQuotaChecker(t *testing.T, checker func(context.Context, quota.Tokens, string) (quota.Snapshot, error)) {
	t.Helper()
	orig := quotaCheckerFactory
	quotaCheckerFactory = func() quotaChecker { return quotaCheckerFunc(checker) }
	t.Cleanup(func() { quotaCheckerFactory = orig })
}

func lineContaining(s, substr string) string {
	for _, line := range strings.Split(s, "\n") {
		if strings.Contains(line, substr) {
			return line
		}
	}
	return ""
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

func TestAuthCommandBacksUpRestoresAndSaves(t *testing.T) {
	dir := t.TempDir()
	profilesDir := filepath.Join(dir, "profiles")
	authPath := filepath.Join(dir, "auth.json")
	cfgPath := filepath.Join(dir, "config.toml")

	originalAuth := []byte(`{"tokens":{"access_token":"old"}}`)
	newAuth := []byte(`{"auth_mode":"chatgpt","tokens":{"access_token":"new"}}`)
	if err := os.WriteFile(authPath, originalAuth, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	runner := &fakeAuthRunner{t: t, authPath: authPath, loginData: newAuth}
	useAuthRunner(t, runner)

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd := NewRootCommand(Dependencies{
		ConfigPath:  cfgPath,
		ProfilesDir: profilesDir,
		AuthPath:    authPath,
		Stdout:      stdout,
		Stderr:      stderr,
	})
	cmd.SetArgs([]string{"auth", "work"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if got, err := os.ReadFile(authPath); err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	} else if string(got) != string(originalAuth) {
		t.Fatalf("auth.json = %s, want %s", got, originalAuth)
	}
	if _, err := os.Stat(authPath + ".bak"); !os.IsNotExist(err) {
		t.Fatalf("auth.json.bak should be removed after success, got err=%v", err)
	}
	if got, err := os.ReadFile(filepath.Join(profilesDir, "work.json")); err != nil {
		t.Fatalf("ReadFile() profile error = %v", err)
	} else if string(got) != string(newAuth) {
		t.Fatalf("profile contents = %s, want %s", got, newAuth)
	}
	gotCfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if gotCfg.ActiveProfile != "work" {
		t.Fatalf("active_profile = %q, want work", gotCfg.ActiveProfile)
	}
	if len(runner.calls) != 2 || strings.Join(runner.calls[0], " ") != "codex logout" || strings.Join(runner.calls[1], " ") != "codex login" {
		t.Fatalf("runner calls = %#v, want logout/login", runner.calls)
	}
	if got := stderr.String(); !strings.Contains(got, "logout") || !strings.Contains(got, "login") {
		t.Fatalf("stderr = %q, want warning before auth flow", got)
	}
}

func TestAuthCommandRejectsExistingProfileWithoutOverwrite(t *testing.T) {
	dir := t.TempDir()
	profilesDir := filepath.Join(dir, "profiles")
	authPath := filepath.Join(dir, "auth.json")
	cfgPath := filepath.Join(dir, "config.toml")

	if err := os.WriteFile(authPath, []byte(`{"tokens":{"access_token":"old"}}`), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	store := profile.NewStore(profilesDir)
	if err := store.Save("work", []byte(`{"tokens":{"access_token":"existing"}}`)); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	runner := &fakeAuthRunner{t: t, authPath: authPath, loginData: []byte(`{"tokens":{"access_token":"new"}}`)}
	useAuthRunner(t, runner)

	stderr := &bytes.Buffer{}
	cmd := NewRootCommand(Dependencies{
		ConfigPath:  cfgPath,
		ProfilesDir: profilesDir,
		AuthPath:    authPath,
		Stderr:      stderr,
	})
	cmd.SetArgs([]string{"auth", "work"})

	if err := cmd.Execute(); err == nil {
		t.Fatal("Execute() error = nil, want non-nil")
	} else if !strings.Contains(err.Error(), "--overwrite") {
		t.Fatalf("Execute() error = %v, want overwrite rejection", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("runner calls = %#v, want none", runner.calls)
	}
	if got, err := os.ReadFile(filepath.Join(profilesDir, "work.json")); err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	} else if string(got) != `{"tokens":{"access_token":"existing"}}` {
		t.Fatalf("profile contents = %s, want existing", got)
	}
	if got, err := os.ReadFile(authPath); err != nil {
		t.Fatalf("ReadFile() auth error = %v", err)
	} else if string(got) != `{"tokens":{"access_token":"old"}}` {
		t.Fatalf("auth.json = %s, want old", got)
	}
	if got := stderr.String(); !strings.Contains(got, "--overwrite") {
		t.Fatalf("stderr = %q, want overwrite rejection", got)
	}
}

func TestAuthCommandOverwriteFlagAllowsReplacingProfile(t *testing.T) {
	dir := t.TempDir()
	profilesDir := filepath.Join(dir, "profiles")
	authPath := filepath.Join(dir, "auth.json")
	cfgPath := filepath.Join(dir, "config.toml")

	if err := os.WriteFile(authPath, []byte(`{"tokens":{"access_token":"old"}}`), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	store := profile.NewStore(profilesDir)
	if err := store.Save("work", []byte(`{"tokens":{"access_token":"existing"}}`)); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	newAuth := []byte(`{"tokens":{"access_token":"new"}}`)
	runner := &fakeAuthRunner{t: t, authPath: authPath, loginData: newAuth}
	useAuthRunner(t, runner)

	cmd := NewRootCommand(Dependencies{
		ConfigPath:  cfgPath,
		ProfilesDir: profilesDir,
		AuthPath:    authPath,
	})
	cmd.SetArgs([]string{"auth", "work", "--overwrite"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(runner.calls) != 2 {
		t.Fatalf("runner calls = %#v, want logout/login", runner.calls)
	}
	if got, err := os.ReadFile(filepath.Join(profilesDir, "work.json")); err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	} else if string(got) != string(newAuth) {
		t.Fatalf("profile contents = %s, want %s", got, newAuth)
	}
}
