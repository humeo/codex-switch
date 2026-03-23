# codex-switch Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a Go CLI that stores multiple Codex OAuth profiles, shows current quota usage, and can automatically switch the active `~/.codex/auth.json` when the current account is nearly depleted.

**Architecture:** Keep the binary thin: Cobra commands in `internal/cli` call focused packages for config, profile storage, quota querying, token refresh, switching, and watch logic. Persist canonical auth copies in `~/.codex-switch/profiles/`; keep user preferences plus cached quota snapshots in `~/.codex-switch/config.toml`; use atomic writes whenever touching `~/.codex/auth.json`.

**Tech Stack:** Go, `github.com/spf13/cobra`, `github.com/pelletier/go-toml/v2`, `github.com/fatih/color`, `github.com/gen2brain/beeep`, standard library `net/http`, `os/exec`, and `encoding/json`.

---

## Planned File Structure

- Create: `.gitignore`
- Create: `go.mod`
- Create: `cmd/codex-switch/main.go`
- Create: `internal/cli/root.go`
- Create: `internal/cli/root_test.go`
- Create: `internal/cli/deps.go`
- Create: `internal/cli/auth.go`
- Create: `internal/cli/list.go`
- Create: `internal/cli/use.go`
- Create: `internal/cli/status.go`
- Create: `internal/cli/watch.go`
- Create: `internal/cli/remove.go`
- Create: `internal/config/config.go`
- Create: `internal/config/config_test.go`
- Create: `internal/profile/profile.go`
- Create: `internal/profile/profile_test.go`
- Create: `internal/quota/quota.go`
- Create: `internal/quota/quota_test.go`
- Create: `internal/auth/refresh.go`
- Create: `internal/auth/refresh_test.go`
- Create: `internal/switcher/switcher.go`
- Create: `internal/switcher/switcher_test.go`
- Create: `internal/watcher/watcher.go`
- Create: `internal/watcher/watcher_test.go`
- Create: `README.md`

## Shared Design Decisions

- Store cached quota data in `config.toml` under per-profile cache sections so `list --no-check` and `status --no-check` can render without adding extra top-level state files.
- Keep `profiles/<name>.json` byte-for-byte compatible with `~/.codex/auth.json`; do not add cache metadata to profile JSON.
- Default `list` behavior is live quota lookup. `--no-check` skips network calls for that invocation. `auto_check = true` controls default behavior and can be disabled globally.
- Treat the check model as config-driven. The binary should ship with a reasonable default string, but all request code must read from config instead of hardcoding the model at call sites.
- Prefer table-driven tests plus `httptest.Server` for quota and refresh flows. Use temp directories for all filesystem tests.

### Task 1: Bootstrap the Repository and CLI Shell

**Files:**
- Create: `.gitignore`
- Create: `go.mod`
- Create: `cmd/codex-switch/main.go`
- Create: `internal/cli/root.go`
- Create: `internal/cli/root_test.go`

- [ ] **Step 1: Initialize git and the Go module**

Run:

```bash
git init
git checkout -b feat/codex-switch-bootstrap
go mod init codex-switch
```

Expected: git repository created, branch is `feat/codex-switch-bootstrap`, and `go.mod` exists with `module codex-switch`.

- [ ] **Step 2: Write the failing root-command test**

```go
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
```

- [ ] **Step 3: Run the test to verify it fails**

Run:

```bash
go test ./internal/cli -run TestRootCommandIncludesCoreSubcommands -v
```

Expected: FAIL because `NewRootCommand` and the subcommands do not exist yet.

- [ ] **Step 4: Implement the minimal CLI shell**

```go
func NewRootCommand(deps Dependencies) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "codex-switch",
		Short: "Manage multiple Codex OAuth profiles",
	}

	cmd.AddCommand(
		newAuthCommand(deps),
		newListCommand(deps),
		newUseCommand(deps),
		newStatusCommand(deps),
		newWatchCommand(deps),
		newRemoveCommand(deps),
	)

	return cmd
}
```

- [ ] **Step 5: Re-run the CLI tests**

Run:

```bash
go test ./internal/cli -run TestRootCommandIncludesCoreSubcommands -v
go test ./...
```

Expected: PASS.

- [ ] **Step 6: Commit the bootstrap**

Run:

```bash
git add .gitignore go.mod cmd/codex-switch/main.go internal/cli/root.go internal/cli/root_test.go
git commit -m "chore: bootstrap codex-switch cli"
```

### Task 2: Implement Config Loading, Defaults, and Cached Quota State

**Files:**
- Create: `internal/config/config.go`
- Create: `internal/config/config_test.go`
- Modify: `internal/cli/deps.go`

- [ ] **Step 1: Write failing tests for default config and round-trip persistence**

```go
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
```

- [ ] **Step 2: Run config tests and confirm failure**

Run:

```bash
go test ./internal/config -v
```

Expected: FAIL because config types and helpers are missing.

- [ ] **Step 3: Implement config types and helpers**

```go
type Config struct {
	CheckModel string                `toml:"check_model"`
	ActiveProfile string             `toml:"active_profile"`
	AutoCheck bool                   `toml:"auto_check"`
	Watch WatchConfig                `toml:"watch"`
	Cache map[string]QuotaCache      `toml:"cache"`
}

func Default() Config {
	return Config{
		CheckModel:   "gpt-5.4-mini",
		ActiveProfile: "",
		AutoCheck:    true,
		Watch: WatchConfig{
			IntervalMinutes:          5,
			PrimaryThresholdPercent:  90,
			SecondaryThresholdPercent: 95,
			Notify:                   true,
		},
		Cache: map[string]QuotaCache{},
	}
}
```

- [ ] **Step 4: Expose config paths through command dependencies**

Add a dependency struct that centralizes:

```go
type Dependencies struct {
	ConfigPath  string
	ProfilesDir string
	AuthPath    string
	Stdout      io.Writer
	Stderr      io.Writer
}
```

- [ ] **Step 5: Re-run config tests**

Run:

```bash
go test ./internal/config -v
go test ./...
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go internal/cli/deps.go
git commit -m "feat: add config persistence and defaults"
```

### Task 3: Implement Profile Storage and Atomic Auth File Helpers

**Files:**
- Create: `internal/profile/profile.go`
- Create: `internal/profile/profile_test.go`
- Create: `internal/switcher/switcher.go`
- Create: `internal/switcher/switcher_test.go`

- [ ] **Step 1: Write failing tests for profile CRUD and atomic auth writes**

```go
func TestSaveAndLoadProfile(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "profiles"))
	raw := []byte(`{"auth_mode":"chatgpt","tokens":{"access_token":"abc"}}`)

	if err := store.Save("work", raw); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	got, err := store.Load("work")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if string(got) != string(raw) {
		t.Fatalf("Load() mismatch = %s", got)
	}
}

func TestWriteAuthAtomicallyReplacesDestination(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")
	if err := os.WriteFile(path, []byte(`old`), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := WriteAuthAtomically(path, []byte(`new`)); err != nil {
		t.Fatalf("WriteAuthAtomically() error = %v", err)
	}
	if got, _ := os.ReadFile(path); string(got) != "new" {
		t.Fatalf("auth contents = %q, want new", got)
	}
}
```

- [ ] **Step 2: Run the package tests**

```bash
go test ./internal/profile ./internal/switcher -v
```

Expected: FAIL.

- [ ] **Step 3: Implement the profile store**

```go
type Store struct {
	dir string
}

func (s Store) Save(name string, raw []byte) error
func (s Store) Load(name string) ([]byte, error)
func (s Store) List() ([]string, error)
func (s Store) Remove(name string) error
```

- [ ] **Step 4: Implement atomic write helpers**

```go
func WriteAuthAtomically(path string, raw []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "auth-*.tmp")
	if err != nil {
		return err
	}
	// write, chmod 0600, close, rename
}
```

- [ ] **Step 5: Re-run tests**

```bash
go test ./internal/profile ./internal/switcher -v
go test ./...
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/profile/profile.go internal/profile/profile_test.go internal/switcher/switcher.go internal/switcher/switcher_test.go
git commit -m "feat: add profile storage and atomic auth writes"
```

### Task 4: Implement Quota Querying and Header Parsing

**Files:**
- Create: `internal/quota/quota.go`
- Create: `internal/quota/quota_test.go`

- [ ] **Step 1: Write failing tests for request shape, header parsing, and 429 handling**

```go
func TestCheckBuildsExpectedRequest(t *testing.T) {
	// httptest server asserts POST /backend-api/codex/responses
	// asserts Authorization, ChatGPT-Account-Id, and JSON body model/input/store/reasoning
}

func TestParseHeadersMapsQuotaValues(t *testing.T) {
	headers := http.Header{
		"X-Codex-Plan-Type": []string{"plus"},
		"X-Codex-Primary-Used-Percent": []string{"12"},
		"X-Codex-Secondary-Used-Percent": []string{"34"},
	}
	got, err := ParseHeaders(headers)
	if err != nil || got.Plan != "plus" || got.PrimaryUsedPercent != 12 || got.SecondaryUsedPercent != 34 {
		t.Fatalf("ParseHeaders() = %#v, %v", got, err)
	}
}

func TestCheckTreatsRateLimitAsFullyUsed(t *testing.T) {
	// return HTTP 429 and assert snapshot reports 100 percent usage
}
```

- [ ] **Step 2: Run quota tests**

```bash
go test ./internal/quota -v
```

Expected: FAIL.

- [ ] **Step 3: Implement the quota client**

```go
type Snapshot struct {
	Plan                   string
	PrimaryUsedPercent     int
	SecondaryUsedPercent   int
	PrimaryResetAfter      time.Duration
	SecondaryResetAfter    time.Duration
	PrimaryResetAt         time.Time
	SecondaryResetAt       time.Time
	HasCredits             bool
	CreditsBalance         string
}

type Client struct {
	BaseURL string
	HTTP    *http.Client
}

func (c Client) Check(ctx context.Context, tokens Tokens, model string) (Snapshot, error)
```

- [ ] **Step 4: Add retry behavior for transient network and Cloudflare-style failures**

Implement a bounded retry loop with small backoff for:
- temporary network errors
- `502`, `503`, `504`
- `403` responses that look like HTML challenge pages

- [ ] **Step 5: Re-run tests**

```bash
go test ./internal/quota -v
go test ./...
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/quota/quota.go internal/quota/quota_test.go
git commit -m "feat: add quota request client"
```

### Task 5: Implement OAuth Token Refresh

**Files:**
- Create: `internal/auth/refresh.go`
- Create: `internal/auth/refresh_test.go`
- Modify: `internal/profile/profile.go`

- [ ] **Step 1: Write failing tests for refresh success and failure**

```go
func TestRefreshUpdatesTokens(t *testing.T) {
	// httptest server returns access_token, id_token, refresh_token, expires_in
	// assert returned token set replaces all three token fields
}

func TestRefreshPropagatesOAuthError(t *testing.T) {
	// server returns 401 or malformed payload, assert error is returned
}
```

- [ ] **Step 2: Run auth tests**

```bash
go test ./internal/auth -v
```

Expected: FAIL.

- [ ] **Step 3: Implement refresh client**

```go
type Refresher struct {
	BaseURL  string
	ClientID string
	HTTP     *http.Client
}

func (r Refresher) Refresh(ctx context.Context, refreshToken string) (profile.Tokens, error)
```

- [ ] **Step 4: Add helpers that update stored profile JSON after refresh**

Implement:

```go
func UpdateTokens(raw []byte, tokens Tokens, refreshedAt time.Time) ([]byte, error)
```

Use it from later quota-check paths so the canonical profile file stays current.

- [ ] **Step 5: Re-run tests**

```bash
go test ./internal/auth -v
go test ./...
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/auth/refresh.go internal/auth/refresh_test.go internal/profile/profile.go
git commit -m "feat: add oauth token refresh"
```

### Task 6: Implement `use` and `remove` Command Behavior

**Files:**
- Modify: `internal/cli/use.go`
- Modify: `internal/cli/remove.go`
- Modify: `internal/switcher/switcher.go`
- Modify: `internal/config/config.go`
- Add tests to: `internal/cli/root_test.go`

- [ ] **Step 1: Write failing command tests**

```go
func TestUseCommandCopiesProfileAndMarksActive(t *testing.T) {
	// prepare temp config + profile file
	// execute `codex-switch use work`
	// assert auth.json matches stored profile and config active_profile == "work"
}

func TestRemoveCommandRejectsActiveProfileWithoutForce(t *testing.T) {
	// assert command returns an error when removing the active profile without --force
}
```

- [ ] **Step 2: Run CLI tests**

```bash
go test ./internal/cli -run 'TestUseCommand|TestRemoveCommand' -v
```

Expected: FAIL.

- [ ] **Step 3: Implement switch and remove flows**

```go
func SwitchProfile(ctx context.Context, cfg *config.Config, store profile.Store, authPath, name string) error
func RemoveProfile(cfg config.Config, store profile.Store, name string, force bool) error
```

- [ ] **Step 4: Wire command flags and friendly output**

Expected behavior:
- `use` prints the selected profile and cached/live quota summary if available
- `remove` prints a confirmation line with the removed profile name

- [ ] **Step 5: Re-run tests**

```bash
go test ./internal/cli -run 'TestUseCommand|TestRemoveCommand' -v
go test ./...
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/cli/use.go internal/cli/remove.go internal/switcher/switcher.go internal/config/config.go internal/cli/root_test.go
git commit -m "feat: add profile switching commands"
```

### Task 7: Implement `auth <name>` Login Capture

**Files:**
- Modify: `internal/cli/auth.go`
- Modify: `internal/profile/profile.go`
- Add tests to: `internal/cli/root_test.go`

- [ ] **Step 1: Write failing tests for backup/restore and overwrite prompts**

```go
func TestAuthCommandCapturesNewLoginAndRestoresOriginalAuth(t *testing.T) {
	// fake exec runner writes a new auth.json into temp ~/.codex
	// assert profile is saved and original auth.json is restored
}

func TestAuthCommandRejectsOverwriteWithoutConfirmation(t *testing.T) {
	// existing profile name should abort without explicit confirmation
}
```

- [ ] **Step 2: Run auth command tests**

```bash
go test ./internal/cli -run 'TestAuthCommand' -v
```

Expected: FAIL.

- [ ] **Step 3: Implement a testable command runner abstraction**

```go
type Runner interface {
	Run(ctx context.Context, name string, args ...string) error
}
```

Use it so tests do not invoke the real `codex` binary.

- [ ] **Step 4: Implement the `auth` flow**

Behavior:
- warn before logout/login
- back up existing `~/.codex/auth.json` when present
- run `codex logout`
- run `codex login`
- save the resulting auth file to `profiles/<name>.json`
- restore the original auth file on success, cancellation, or failure
- set `active_profile` if this is the first saved profile

- [ ] **Step 5: Re-run tests**

```bash
go test ./internal/cli -run 'TestAuthCommand' -v
go test ./...
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/cli/auth.go internal/profile/profile.go internal/cli/root_test.go
git commit -m "feat: add interactive auth import command"
```

### Task 8: Implement `list` and `status` Commands

**Files:**
- Modify: `internal/cli/list.go`
- Modify: `internal/cli/status.go`
- Modify: `internal/quota/quota.go`
- Modify: `internal/config/config.go`
- Add tests to: `internal/cli/root_test.go`

- [ ] **Step 1: Write failing tests for live lookup, `--no-check`, and empty state**

```go
func TestListCommandShowsEmptyState(t *testing.T) {
	// assert no-profile message is printed
}

func TestListCommandUsesLiveQuotaByDefault(t *testing.T) {
	// fake quota client returns snapshots for two profiles
	// assert output is sorted by weekly usage ascending
}

func TestListCommandNoCheckUsesCachedSnapshot(t *testing.T) {
	// config cache has a saved snapshot and quota client is never called
}

func TestStatusCommandShowsActiveProfileDetails(t *testing.T) {
	// assert formatted plan, usage, and reset timestamps are rendered
}
```

- [ ] **Step 2: Run list/status tests**

```bash
go test ./internal/cli -run 'TestListCommand|TestStatusCommand' -v
```

Expected: FAIL.

- [ ] **Step 3: Implement command behavior**

Key rules:
- `list` checks live quotas unless `--no-check` is set or config `auto_check` is false
- live snapshots update the config cache
- `status` targets the active profile and supports `--no-check`
- expired profiles render a warning indicator instead of causing the whole command to fail

- [ ] **Step 4: Keep formatting logic isolated**

Add small helpers for:
- relative reset durations
- active marker
- plan / credits rendering

Keep the table formatting deterministic for tests.

- [ ] **Step 5: Re-run tests**

```bash
go test ./internal/cli -run 'TestListCommand|TestStatusCommand' -v
go test ./...
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/cli/list.go internal/cli/status.go internal/quota/quota.go internal/config/config.go internal/cli/root_test.go
git commit -m "feat: add quota listing and status commands"
```

### Task 9: Implement Watcher Auto-Switch Logic

**Files:**
- Create: `internal/watcher/watcher.go`
- Create: `internal/watcher/watcher_test.go`
- Modify: `internal/cli/watch.go`
- Modify: `internal/switcher/switcher.go`

- [ ] **Step 1: Write failing tests for threshold evaluation and best-profile selection**

```go
func TestRunSwitchesWhenBetterProfileExists(t *testing.T) {
	// current profile crosses threshold, second profile has lower weekly usage
	// assert switch callback is invoked with the better profile
}

func TestRunDoesNotPingPongToWorseProfile(t *testing.T) {
	// candidate has equal or higher weekly usage; assert no switch
}

func TestRunReportsAllAccountsDepleted(t *testing.T) {
	// all profiles exceed current profile weekly usage; assert notification path fires
}
```

- [ ] **Step 2: Run watcher tests**

```bash
go test ./internal/watcher -v
```

Expected: FAIL.

- [ ] **Step 3: Implement watcher loop and selection rules**

```go
type Service struct {
	QuotaClient QuotaChecker
	Switcher    ProfileSwitcher
	Notifier    Notifier
	Logger      io.Writer
}

func (s Service) RunOnce(ctx context.Context, cfg config.Config, active string, names []string) error
func (s Service) Run(ctx context.Context, cfg config.Config, active string, names []string) error
```

- [ ] **Step 4: Wire the `watch` command**

Behavior:
- runs in foreground
- prints interval + threshold settings on start
- traps context cancellation cleanly
- only sends desktop notifications when config `watch.notify` is true

- [ ] **Step 5: Re-run tests**

```bash
go test ./internal/watcher -v
go test ./...
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/watcher/watcher.go internal/watcher/watcher_test.go internal/cli/watch.go internal/switcher/switcher.go
git commit -m "feat: add automatic quota watcher"
```

### Task 10: Integration Polish, Docs, and Smoke Verification

**Files:**
- Create: `README.md`
- Modify: `cmd/codex-switch/main.go`
- Modify: `internal/cli/*.go` as needed for help text polish

- [ ] **Step 1: Write a smoke test for `--help` output**

```go
func TestRootHelpMentionsQuotaAndSwitching(t *testing.T) {
	// execute root command with --help and assert key phrases appear
}
```

- [ ] **Step 2: Run the smoke test and confirm failure if help text is incomplete**

```bash
go test ./internal/cli -run TestRootHelpMentionsQuotaAndSwitching -v
```

Expected: FAIL or weak assertions before polish.

- [ ] **Step 3: Add README usage examples**

Cover:
- `auth <name>`
- `list` and `list --no-check`
- `use <name>`
- `status`
- `watch`
- `remove <name>`
- config file example
- warning that `auth` temporarily invalidates the current Codex session

- [ ] **Step 4: Run full verification**

```bash
go test ./...
go test -race ./...
go run ./cmd/codex-switch --help
```

Expected: all tests pass, race detector passes, and help output prints without panic.

- [ ] **Step 5: Commit the final polish**

```bash
git add README.md cmd/codex-switch/main.go internal/cli
git commit -m "docs: finalize codex-switch usage and polish"
```

## Final Verification Checklist

- [ ] `go test ./...`
- [ ] `go test -race ./...`
- [ ] `go run ./cmd/codex-switch --help`
- [ ] Manual smoke check with temp directories for:
  - `auth <name>` backup and restore
  - `list --no-check`
  - `use <name>`
  - `remove <name> --force`
  - `watch` one-shot run with fake quota client

## Open Questions to Resolve During Execution

- If the actual cheapest viable model name changes, update only `config.Default()` and keep all request code model-agnostic.
- If `codex login` does not return until browser completion, keep the current runner abstraction. If it daemonizes differently, extend the runner only after confirming with a focused test.
- If desktop notifications are not available on a target OS, the watcher should log the failure but continue running.
