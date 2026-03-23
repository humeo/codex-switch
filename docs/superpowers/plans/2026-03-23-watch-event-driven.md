# Event-Driven Watch Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace interval-based `watch` polling with startup-only calibration plus local Codex session event triggers that confirm and switch profiles only when thresholds are exceeded.

**Architecture:** Keep direct quota probes as the authority, but move watch scheduling to a lightweight local session-file monitor. Persist watcher runtime state plus rolling probe logs under `~/.codex-switch/`, and retain only the last 7 days of watch logs/check history.

**Tech Stack:** Go, Cobra, standard library filesystem and JSON tooling, existing `internal/quota`, `internal/profile`, and `internal/switcher` packages.

---

## Planned File Structure

- Modify: `internal/cli/deps.go`
- Modify: `internal/cli/watch.go`
- Modify: `internal/watcher/watcher.go`
- Modify: `internal/watcher/watcher_test.go`
- Create: `internal/watcher/events.go`
- Create: `internal/watcher/events_test.go`
- Create: `internal/watcher/state.go`
- Create: `internal/watcher/state_test.go`
- Modify: `README.md`

## Shared Design Decisions

- Do not use `codex exec` for watch decisions. Session files are only triggers; direct HTTP probes remain authoritative.
- Do not add network polling after startup calibration. Any repeated local file scanning is acceptable because it does not spend quota.
- Keep retention logic focused on `watch-checks.jsonl` and `watch.log`, each with a 7-day cutoff.
- Reuse existing threshold comparison and candidate ranking logic rather than inventing new switching rules.

### Task 1: Add Watch State and Probe History Persistence

**Files:**
- Create: `internal/watcher/state.go`
- Create: `internal/watcher/state_test.go`
- Modify: `internal/cli/deps.go`

- [ ] **Step 1: Write the failing state persistence tests**

Add tests for:

```go
func TestLoadStateMissingFileReturnsDefault(t *testing.T)
func TestSaveStateRoundTripsSamplesAndRuntime(t *testing.T)
func TestPruneJSONLFileDropsEntriesOlderThanRetention(t *testing.T)
```

- [ ] **Step 2: Run the watcher tests to verify failure**

Run:

```bash
go test ./internal/watcher -run 'TestLoadStateMissingFileReturnsDefault|TestSaveStateRoundTripsSamplesAndRuntime|TestPruneJSONLFileDropsEntriesOlderThanRetention' -v
```

Expected: FAIL because state helpers and retention code do not exist.

- [ ] **Step 3: Implement minimal watch state and retention helpers**

Add:

- `WatchState`, `RuntimeState`, `ProfileState`, `SnapshotSample`
- `LoadState(path string) (WatchState, error)`
- `SaveState(path string, state WatchState) error`
- `AppendCheckEvent(path string, event CheckEvent) error`
- `PruneJSONLFile(path string, cutoff time.Time) error`
- `PruneLogFile(path string, cutoff time.Time) error`

Update `internal/cli/deps.go` to expose:

- `CodexSessionsDir`
- `WatchStatePath`
- `WatchChecksPath`
- `WatchLogPath`

- [ ] **Step 4: Re-run the focused watcher tests**

Run:

```bash
go test ./internal/watcher -run 'TestLoadStateMissingFileReturnsDefault|TestSaveStateRoundTripsSamplesAndRuntime|TestPruneJSONLFileDropsEntriesOlderThanRetention' -v
```

Expected: PASS.

### Task 2: Implement Session Event Parsing and Event-Driven Watch Service

**Files:**
- Create: `internal/watcher/events.go`
- Create: `internal/watcher/events_test.go`
- Modify: `internal/watcher/watcher.go`
- Modify: `internal/watcher/watcher_test.go`

- [ ] **Step 1: Write the failing event parser tests**

Add tests for:

```go
func TestParseTokenCountEventExtractsRateLimits(t *testing.T)
func TestParseTokenCountEventIgnoresMissingRateLimits(t *testing.T)
func TestSessionReaderStreamsOnlyValidTokenCountEvents(t *testing.T)
```

- [ ] **Step 2: Run the focused event tests to verify failure**

Run:

```bash
go test ./internal/watcher -run 'TestParseTokenCountEventExtractsRateLimits|TestParseTokenCountEventIgnoresMissingRateLimits|TestSessionReaderStreamsOnlyValidTokenCountEvents' -v
```

Expected: FAIL because session event parsing does not exist.

- [ ] **Step 3: Implement minimal event parsing and local session scanning**

Add:

- `TokenCountEvent` model that reads `event_msg.payload.type == "token_count"`
- parsing for `payload.rate_limits.primary.used_percent` and `payload.rate_limits.secondary.used_percent`
- a local session monitor that tails `~/.codex/sessions/**/*.jsonl` and emits valid `TokenCountEvent`s

Keep the implementation standard-library only; local scan intervals are fine because they do not consume quota.

- [ ] **Step 4: Write the failing service behavior tests**

Add tests for:

```go
func TestRunPerformsStartupCalibrationOnce(t *testing.T)
func TestTriggeredEventConfirmsActiveBeforeCheckingCandidates(t *testing.T)
func TestTriggeredEventDoesNotProbeCandidatesWhenConfirmationFallsBelowThreshold(t *testing.T)
func TestSwitchUpdatesActiveProfileAndEnforcesCooldown(t *testing.T)
```

- [ ] **Step 5: Run the focused service tests to verify failure**

Run:

```bash
go test ./internal/watcher -run 'TestRunPerformsStartupCalibrationOnce|TestTriggeredEventConfirmsActiveBeforeCheckingCandidates|TestTriggeredEventDoesNotProbeCandidatesWhenConfirmationFallsBelowThreshold|TestSwitchUpdatesActiveProfileAndEnforcesCooldown' -v
```

Expected: FAIL because `watcher.Service` is still interval-based.

- [ ] **Step 6: Implement the minimal event-driven watcher**

Change `internal/watcher/watcher.go` so that:

- `Run` performs one startup calibration
- runtime reads local session trigger events instead of using `time.Ticker`
- thresholded trigger events cause active-profile confirmation
- candidate probes only happen after confirmed over-threshold active state
- successful switches update the in-memory active profile and state file
- repeated triggers inside cooldown are ignored

- [ ] **Step 7: Re-run the focused watcher tests**

Run:

```bash
go test ./internal/watcher -run 'TestParseTokenCountEventExtractsRateLimits|TestParseTokenCountEventIgnoresMissingRateLimits|TestSessionReaderStreamsOnlyValidTokenCountEvents|TestRunPerformsStartupCalibrationOnce|TestTriggeredEventConfirmsActiveBeforeCheckingCandidates|TestTriggeredEventDoesNotProbeCandidatesWhenConfirmationFallsBelowThreshold|TestSwitchUpdatesActiveProfileAndEnforcesCooldown' -v
```

Expected: PASS.

### Task 3: Wire the CLI Command and Update User-Facing Docs

**Files:**
- Modify: `internal/cli/watch.go`
- Modify: `README.md`

- [ ] **Step 1: Write the failing CLI watch command test**

Add a test for:

```go
func TestWatchCommandPrintsEventDrivenStartupMode(t *testing.T)
```

Expected behavior: watch startup output mentions session-event monitoring and startup calibration instead of fixed interval polling.

- [ ] **Step 2: Run the focused CLI test to verify failure**

Run:

```bash
go test ./internal/cli -run TestWatchCommandPrintsEventDrivenStartupMode -v
```

Expected: FAIL because the command still prints interval-based messaging.

- [ ] **Step 3: Implement minimal CLI wiring**

Update `internal/cli/watch.go` to:

- pass new dependency paths into the watcher service
- load notifier as before
- print event-driven startup messaging
- stop referencing `interval_minutes` at runtime

- [ ] **Step 4: Update README watch behavior**

Document:

- startup-only calibration
- session-event trigger behavior
- direct probe confirmation before switching
- 7-day watch log/check retention

- [ ] **Step 5: Run package and full-repo verification**

Run:

```bash
go test ./internal/cli ./internal/watcher -v
go test ./...
```

Expected: PASS.

- [ ] **Step 6: Commit the completed implementation**

Run:

```bash
git add internal/cli/deps.go internal/cli/watch.go internal/watcher/events.go internal/watcher/events_test.go internal/watcher/state.go internal/watcher/state_test.go internal/watcher/watcher.go internal/watcher/watcher_test.go README.md docs/superpowers/plans/2026-03-23-watch-event-driven.md
git commit -m "feat: make watch event-driven"
```
