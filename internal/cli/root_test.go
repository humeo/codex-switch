package cli

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex-switch/internal/config"
	"codex-switch/internal/profile"
	"codex-switch/internal/quota"
	"codex-switch/internal/watcher"
)

type fakeAuthRunner struct {
	t         *testing.T
	authPath  string
	loginData []byte
	calls     [][]string
	err       error
}

func (r *fakeAuthRunner) Run(_ context.Context, name string, args ...string) error {
	r.calls = append(r.calls, append([]string{name}, args...))
	if name != "codex" {
		r.t.Fatalf("runner name = %q, want codex", name)
	}
	if r.err != nil {
		return r.err
	}
	if len(args) == 1 && args[0] == "login" {
		if err := os.WriteFile(r.authPath, r.loginData, 0o600); err != nil {
			r.t.Fatalf("WriteFile() error = %v", err)
		}
	}
	return nil
}

type fakeAuthProber struct {
	email string
	err   error
	raws  [][]byte
}

func (p *fakeAuthProber) Probe(_ context.Context, raw []byte, _ string) (authIdentity, error) {
	p.raws = append(p.raws, append([]byte(nil), raw...))
	if p.err != nil {
		return authIdentity{}, p.err
	}
	return authIdentity{Email: p.email}, nil
}

func useAuthRunner(t *testing.T, runner Runner) {
	t.Helper()
	orig := authRunnerFactory
	authRunnerFactory = func() Runner { return runner }
	t.Cleanup(func() { authRunnerFactory = orig })
}

func useAuthProber(t *testing.T, prober authProber) {
	t.Helper()
	orig := authProberFactory
	authProberFactory = func() authProber { return prober }
	t.Cleanup(func() { authProberFactory = orig })
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

func TestRootHelpMentionsQuotaAndSwitching(t *testing.T) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd := NewRootCommand(Dependencies{
		Stdout: stdout,
		Stderr: stderr,
	})
	cmd.SetArgs([]string{"--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	got := stdout.String()
	for _, want := range []string{
		"Manage multiple Codex OAuth profiles",
		"List stored profiles and quota status",
		"Watch quota usage and switch profiles automatically",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("help output missing %q\n%s", want, got)
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
	want := "No profiles found. Run 'codex-switch auth --login <name>' to add one."
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
	cfg.AutoCheck = true
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
	for _, want := range []string{"NAME", "5H USED", "5H LEFT", "WEEKLY USED", "WEEKLY LEFT", "SRC"} {
		if !strings.Contains(got, want) {
			t.Fatalf("stdout = %q, want list header %q", got, want)
		}
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
	if !strings.Contains(betaLine, "88%") || !strings.Contains(alphaLine, "70%") {
		t.Fatalf("stdout = %q, want remaining quota percentages", got)
	}
	if !strings.Contains(betaLine, "live") || !strings.Contains(alphaLine, "live") {
		t.Fatalf("stdout = %q, want live source markers", got)
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

func TestListCommandChecksLiveByDefault(t *testing.T) {
	dir := t.TempDir()
	profilesDir := filepath.Join(dir, "profiles")

	store := profile.NewStore(profilesDir)
	writeProfile(t, store, "cached", []byte(`{"tokens":{"access_token":"cached-token","account_id":"acct-cached"}}`))

	called := false
	useQuotaChecker(t, func(_ context.Context, _ quota.Tokens, _ string) (quota.Snapshot, error) {
		called = true
		return quota.Snapshot{
			Plan:                 "plus",
			PrimaryUsedPercent:   3,
			SecondaryUsedPercent: 9,
			PrimaryResetAfter:    2 * time.Hour,
			SecondaryResetAfter:  24 * time.Hour,
		}, nil
	})

	stdout := &bytes.Buffer{}
	cmd := NewRootCommand(Dependencies{
		ConfigPath:  filepath.Join(dir, "config.toml"),
		ProfilesDir: profilesDir,
		Stdout:      stdout,
	})
	cmd.SetArgs([]string{"list"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if !called {
		t.Fatal("quota checker not called, want live output by default")
	}
	got := stdout.String()
	for _, want := range []string{"cached", "plus", "3%", "9%"} {
		if !strings.Contains(got, want) {
			t.Fatalf("stdout = %q, want %q", got, want)
		}
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
	for _, want := range []string{"cached", "plus", "7%", "93%", "14%", "86%", "cache"} {
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

func TestWatchCommandPrintsEventDrivenStartupMode(t *testing.T) {
	dir := t.TempDir()
	profilesDir := filepath.Join(dir, "profiles")
	cfgPath := filepath.Join(dir, "config.toml")
	statePath := filepath.Join(dir, "watch-state.toml")
	checksPath := filepath.Join(dir, "watch-checks.jsonl")
	logPath := filepath.Join(dir, "watch.log")

	store := profile.NewStore(profilesDir)
	writeProfile(t, store, "alpha", []byte(`{"tokens":{"access_token":"alpha-token","account_id":"acct-alpha"}}`))

	cfg := config.Default()
	cfg.ActiveProfile = "alpha"
	cfg.Watch.Notify = false
	if err := config.Save(cfgPath, cfg); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	useWatchQuotaChecker(t, func(_ context.Context, tokens quota.Tokens, model string) (quota.Snapshot, error) {
		if tokens.AccessToken != "alpha-token" {
			t.Fatalf("access token = %q, want alpha-token", tokens.AccessToken)
		}
		if model != cfg.CheckModel {
			t.Fatalf("model = %q, want %q", model, cfg.CheckModel)
		}
		return quota.Snapshot{
			Plan:                 "plus",
			PrimaryUsedPercent:   13,
			SecondaryUsedPercent: 40,
		}, nil
	})

	events := make(chan watcher.TokenCountEvent)
	close(events)

	stdout := &bytes.Buffer{}
	cmd := NewRootCommand(Dependencies{
		ConfigPath:         cfgPath,
		ProfilesDir:        profilesDir,
		AuthPath:           filepath.Join(dir, "auth.json"),
		CodexSessionsDir:   filepath.Join(dir, "sessions"),
		WatchStatePath:     statePath,
		WatchChecksPath:    checksPath,
		WatchLogPath:       logPath,
		WatchTriggerEvents: events,
		Stdout:             stdout,
	})
	cmd.SetArgs([]string{"watch"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	got := stdout.String()
	if !strings.Contains(got, "watching local Codex session events") {
		t.Fatalf("stdout = %q, want session event mode", got)
	}
	if !strings.Contains(got, "startup calibration complete for alpha") {
		t.Fatalf("stdout = %q, want startup calibration summary", got)
	}
	if strings.Contains(got, "watching every") {
		t.Fatalf("stdout = %q, want no interval polling output", got)
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

func useTerminalDetector(t *testing.T, detector func(*os.File) bool) {
	t.Helper()
	orig := useIsTerminal
	useIsTerminal = detector
	t.Cleanup(func() { useIsTerminal = orig })
}

func useInteractiveSelector(t *testing.T, selector func(*os.File, io.Writer, []listRow) (string, error)) {
	t.Helper()
	orig := useSelectProfile
	useSelectProfile = selector
	t.Cleanup(func() { useSelectProfile = orig })
}

type fakeWatchQuotaChecker func(context.Context, quota.Tokens, string) (quota.Snapshot, error)

func (f fakeWatchQuotaChecker) Check(ctx context.Context, tokens quota.Tokens, model string) (quota.Snapshot, error) {
	return f(ctx, tokens, model)
}

func useWatchQuotaChecker(t *testing.T, checker fakeWatchQuotaChecker) {
	t.Helper()
	orig := watchQuotaCheckerFactory
	watchQuotaCheckerFactory = func() watcher.QuotaChecker { return checker }
	t.Cleanup(func() { watchQuotaCheckerFactory = orig })
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

func TestUseCommandWithoutArgsRequiresInteractiveTerminal(t *testing.T) {
	dir := t.TempDir()
	profilesDir := filepath.Join(dir, "profiles")
	authPath := filepath.Join(dir, "auth.json")
	cfgPath := filepath.Join(dir, "config.toml")

	store := profile.NewStore(profilesDir)
	if err := store.Save("work", []byte(`{"auth_mode":"chatgpt","tokens":{"access_token":"abc"}}`)); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if err := config.Save(cfgPath, config.Default()); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	useTerminalDetector(t, func(_ *os.File) bool { return false })

	input, err := os.CreateTemp(t.TempDir(), "stdin")
	if err != nil {
		t.Fatalf("CreateTemp() error = %v", err)
	}
	t.Cleanup(func() { _ = input.Close() })

	cmd := NewRootCommand(Dependencies{
		ConfigPath:  cfgPath,
		ProfilesDir: profilesDir,
		AuthPath:    authPath,
		Stdin:       input,
	})
	cmd.SetArgs([]string{"use"})

	err = cmd.Execute()
	if err == nil {
		t.Fatal("Execute() error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), "interactive terminal") {
		t.Fatalf("Execute() error = %v, want interactive terminal guidance", err)
	}
}

func TestUseCommandWithoutArgsUsesInteractiveSelector(t *testing.T) {
	dir := t.TempDir()
	profilesDir := filepath.Join(dir, "profiles")
	authPath := filepath.Join(dir, "auth.json")
	cfgPath := filepath.Join(dir, "config.toml")

	store := profile.NewStore(profilesDir)
	alphaRaw := []byte(`{"auth_mode":"chatgpt","tokens":{"access_token":"alpha-token","account_id":"acct-alpha"}}`)
	betaRaw := []byte(`{"auth_mode":"chatgpt","tokens":{"access_token":"beta-token","account_id":"acct-beta"}}`)
	if err := store.Save("alpha", alphaRaw); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if err := store.Save("beta", betaRaw); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	cfg := config.Default()
	cfg.ActiveProfile = "alpha"
	if err := config.Save(cfgPath, cfg); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	useTerminalDetector(t, func(_ *os.File) bool { return true })

	var captured []listRow
	useInteractiveSelector(t, func(_ *os.File, _ io.Writer, rows []listRow) (string, error) {
		captured = append([]listRow(nil), rows...)
		return "beta", nil
	})

	useQuotaChecker(t, func(_ context.Context, tokens quota.Tokens, _ string) (quota.Snapshot, error) {
		switch tokens.AccessToken {
		case "alpha-token":
			return quota.Snapshot{Plan: "plus", PrimaryUsedPercent: 8, SecondaryUsedPercent: 89}, nil
		case "beta-token":
			return quota.Snapshot{Plan: "team", PrimaryUsedPercent: 3, SecondaryUsedPercent: 31}, nil
		default:
			t.Fatalf("unexpected access token %q", tokens.AccessToken)
			return quota.Snapshot{}, nil
		}
	})

	input, err := os.CreateTemp(t.TempDir(), "stdin")
	if err != nil {
		t.Fatalf("CreateTemp() error = %v", err)
	}
	t.Cleanup(func() { _ = input.Close() })

	stdout := &bytes.Buffer{}
	cmd := NewRootCommand(Dependencies{
		ConfigPath:  cfgPath,
		ProfilesDir: profilesDir,
		AuthPath:    authPath,
		Stdout:      stdout,
		Stdin:       input,
	})
	cmd.SetArgs([]string{"use"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if len(captured) != 2 {
		t.Fatalf("captured rows = %#v, want 2 rows", captured)
	}
	if !captured[1].active {
		t.Fatalf("captured rows = %#v, want alpha marked active before selection", captured)
	}
	if captured[0].name != "beta" || captured[0].snapshot.Plan != "team" {
		t.Fatalf("captured rows = %#v, want sorted beta row first with quota data", captured)
	}
	if captured[0].source != quotaSourceLive || captured[1].source != quotaSourceLive {
		t.Fatalf("captured rows = %#v, want live source markers", captured)
	}
	gotAuth, err := os.ReadFile(authPath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(gotAuth) != string(betaRaw) {
		t.Fatalf("auth.json = %s, want %s", gotAuth, betaRaw)
	}
	gotCfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if gotCfg.ActiveProfile != "beta" {
		t.Fatalf("active_profile = %q, want beta", gotCfg.ActiveProfile)
	}
	if got := stdout.String(); !strings.Contains(got, "active profile: beta") {
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

func TestAuthCommandLogsInWhenNoCurrentAuthAndUsesExplicitName(t *testing.T) {
	dir := t.TempDir()
	profilesDir := filepath.Join(dir, "profiles")
	authPath := filepath.Join(dir, "auth.json")
	cfgPath := filepath.Join(dir, "config.toml")

	newAuth := []byte(`{"auth_mode":"chatgpt","tokens":{"access_token":"new"}}`)
	runner := &fakeAuthRunner{t: t, authPath: authPath, loginData: newAuth}
	prober := &fakeAuthProber{email: "koltenluca433@gmail.com"}
	useAuthRunner(t, runner)
	useAuthProber(t, prober)

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd := NewRootCommand(Dependencies{
		ConfigPath:  cfgPath,
		ProfilesDir: profilesDir,
		AuthPath:    authPath,
		Stdout:      stdout,
		Stderr:      stderr,
	})
	cmd.SetArgs([]string{"auth", "--login", "--name", "work"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if _, err := os.Stat(authPath); !os.IsNotExist(err) {
		t.Fatalf("auth.json should be removed after import when no original auth existed, got err=%v", err)
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
	if len(prober.raws) != 1 || string(prober.raws[0]) != string(newAuth) {
		t.Fatalf("prober raws = %#v, want imported auth payload", prober.raws)
	}
	if got := stderr.String(); !strings.Contains(got, "logout") || !strings.Contains(got, "login") {
		t.Fatalf("stderr = %q, want warning before auth flow", got)
	}
}

func TestAuthCommandWithoutCurrentAuthSuggestsLoginFlag(t *testing.T) {
	dir := t.TempDir()
	profilesDir := filepath.Join(dir, "profiles")
	authPath := filepath.Join(dir, "auth.json")
	cfgPath := filepath.Join(dir, "config.toml")

	runner := &fakeAuthRunner{t: t, authPath: authPath}
	prober := &fakeAuthProber{email: "koltenluca433@gmail.com"}
	useAuthRunner(t, runner)
	useAuthProber(t, prober)

	stderr := &bytes.Buffer{}
	cmd := NewRootCommand(Dependencies{
		ConfigPath:  cfgPath,
		ProfilesDir: profilesDir,
		AuthPath:    authPath,
		Stderr:      stderr,
	})
	cmd.SetArgs([]string{"auth", "--name", "work"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute() error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), "--login") {
		t.Fatalf("Execute() error = %v, want --login guidance", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("runner calls = %#v, want none", runner.calls)
	}
	if len(prober.raws) != 0 {
		t.Fatalf("prober raws = %#v, want none", prober.raws)
	}
	if got := stderr.String(); !strings.Contains(got, "--login") {
		t.Fatalf("stderr = %q, want --login guidance", got)
	}
}

func TestAuthCommandUsesCurrentSessionAndDerivedEmail(t *testing.T) {
	dir := t.TempDir()
	profilesDir := filepath.Join(dir, "profiles")
	authPath := filepath.Join(dir, "auth.json")
	cfgPath := filepath.Join(dir, "config.toml")

	currentAuth := []byte(`{"tokens":{"access_token":"old"}}`)
	if err := os.WriteFile(authPath, currentAuth, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	runner := &fakeAuthRunner{t: t, authPath: authPath}
	prober := &fakeAuthProber{email: "koltenluca433@gmail.com"}
	useAuthRunner(t, runner)
	useAuthProber(t, prober)

	cmd := NewRootCommand(Dependencies{
		ConfigPath:  cfgPath,
		ProfilesDir: profilesDir,
		AuthPath:    authPath,
	})
	cmd.SetArgs([]string{"auth"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("runner calls = %#v, want none", runner.calls)
	}
	if got, err := os.ReadFile(authPath); err != nil {
		t.Fatalf("ReadFile() auth error = %v", err)
	} else if string(got) != string(currentAuth) {
		t.Fatalf("auth.json = %s, want %s", got, currentAuth)
	}
	if got, err := os.ReadFile(filepath.Join(profilesDir, "koltenluca433@gmail.com.json")); err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	} else if string(got) != string(currentAuth) {
		t.Fatalf("profile contents = %s, want %s", got, currentAuth)
	}
}

func TestLiveAuthProberSendsStreamingResponseRequest(t *testing.T) {
	prober := liveAuthProber{
		HTTP: &http.Client{
			Transport: authRoundTripperFunc(func(req *http.Request) (*http.Response, error) {
				if req.Method != http.MethodPost {
					t.Fatalf("method = %s, want POST", req.Method)
				}
				if req.URL.Path != "/backend-api/codex/responses" {
					t.Fatalf("path = %s, want /backend-api/codex/responses", req.URL.Path)
				}
				if got := req.Header.Get("Authorization"); got != "Bearer access-token" {
					t.Fatalf("Authorization = %q, want Bearer access-token", got)
				}
				if got := req.Header.Get("ChatGPT-Account-Id"); got != "account-123" {
					t.Fatalf("ChatGPT-Account-Id = %q, want account-123", got)
				}

				body, err := io.ReadAll(req.Body)
				if err != nil {
					t.Fatalf("reading body: %v", err)
				}
				gotBody := string(body)
				for _, want := range []string{
					`"model":"gpt-4.1"`,
					`"input":[{`,
					`"role":"user"`,
					`"content":"hi"`,
					`"instructions":"."`,
					`"store":false`,
					`"stream":true`,
					`"reasoning":{"effort":"none"}`,
				} {
					if !strings.Contains(gotBody, want) {
						t.Fatalf("request body %s missing %s", gotBody, want)
					}
				}

				headers := http.Header{}
				headers.Set("X-Auth-Email", "koltenluca433@gmail.com")
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     headers,
					Body:       io.NopCloser(strings.NewReader("{}")),
				}, nil
			}),
		},
	}

	got, err := prober.Probe(context.Background(), []byte(`{"tokens":{"access_token":"access-token","account_id":"account-123"}}`), "gpt-4.1")
	if err != nil {
		t.Fatalf("Probe() error = %v", err)
	}
	if got.Email != "koltenluca433@gmail.com" {
		t.Fatalf("Email = %q, want koltenluca433@gmail.com", got.Email)
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

	runner := &fakeAuthRunner{t: t, authPath: authPath}
	prober := &fakeAuthProber{email: "work"}
	useAuthRunner(t, runner)
	useAuthProber(t, prober)

	stderr := &bytes.Buffer{}
	cmd := NewRootCommand(Dependencies{
		ConfigPath:  cfgPath,
		ProfilesDir: profilesDir,
		AuthPath:    authPath,
		Stderr:      stderr,
	})
	cmd.SetArgs([]string{"auth"})

	if err := cmd.Execute(); err == nil {
		t.Fatal("Execute() error = nil, want non-nil")
	} else if !strings.Contains(err.Error(), "--overwrite") || !strings.Contains(err.Error(), "--login") {
		t.Fatalf("Execute() error = %v, want overwrite and login guidance", err)
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
	if got := stderr.String(); !strings.Contains(got, "--overwrite") || !strings.Contains(got, "--login") {
		t.Fatalf("stderr = %q, want overwrite and login guidance", got)
	}
}

type authRoundTripperFunc func(*http.Request) (*http.Response, error)

func (fn authRoundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func TestAuthCommandLoginFlagReplacesTemporarySessionAndRestoresOriginalAuth(t *testing.T) {
	dir := t.TempDir()
	profilesDir := filepath.Join(dir, "profiles")
	authPath := filepath.Join(dir, "auth.json")
	cfgPath := filepath.Join(dir, "config.toml")

	originalAuth := []byte(`{"tokens":{"access_token":"original"}}`)
	if err := os.WriteFile(authPath, originalAuth, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	newAuth := []byte(`{"tokens":{"access_token":"new"}}`)
	runner := &fakeAuthRunner{t: t, authPath: authPath, loginData: newAuth}
	prober := &fakeAuthProber{email: "new-account@example.com"}
	useAuthRunner(t, runner)
	useAuthProber(t, prober)

	cmd := NewRootCommand(Dependencies{
		ConfigPath:  cfgPath,
		ProfilesDir: profilesDir,
		AuthPath:    authPath,
	})
	cmd.SetArgs([]string{"auth", "--login", "--name", "other"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(runner.calls) != 2 || strings.Join(runner.calls[0], " ") != "codex logout" || strings.Join(runner.calls[1], " ") != "codex login" {
		t.Fatalf("runner calls = %#v, want logout/login", runner.calls)
	}
	if got, err := os.ReadFile(authPath); err != nil {
		t.Fatalf("ReadFile() auth error = %v", err)
	} else if string(got) != string(originalAuth) {
		t.Fatalf("auth.json = %s, want %s", got, originalAuth)
	}
	if got, err := os.ReadFile(filepath.Join(profilesDir, "other.json")); err != nil {
		t.Fatalf("ReadFile() profile error = %v", err)
	} else if string(got) != string(newAuth) {
		t.Fatalf("profile contents = %s, want %s", got, newAuth)
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
	runner := &fakeAuthRunner{t: t, authPath: authPath}
	prober := &fakeAuthProber{email: "work"}
	useAuthRunner(t, runner)
	useAuthProber(t, prober)

	cmd := NewRootCommand(Dependencies{
		ConfigPath:  cfgPath,
		ProfilesDir: profilesDir,
		AuthPath:    authPath,
	})
	if err := os.WriteFile(authPath, newAuth, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	cmd.SetArgs([]string{"auth", "--overwrite"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("runner calls = %#v, want none", runner.calls)
	}
	if got, err := os.ReadFile(filepath.Join(profilesDir, "work.json")); err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	} else if string(got) != string(newAuth) {
		t.Fatalf("profile contents = %s, want %s", got, newAuth)
	}
}
