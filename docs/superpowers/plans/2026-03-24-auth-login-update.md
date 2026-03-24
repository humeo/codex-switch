# Auth Login and Update Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `auth --login` preserve a usable active Codex auth state, print a manual login URL when available, and add a native `codex-switch update` command for release-installed users.

**Architecture:** Keep the auth bugfix in the existing `internal/cli/auth.go` flow so the active-profile decision remains close to login/import logic. Add a focused update implementation under `internal/cli` and reuse the repository's atomic file replacement patterns instead of shelling out to `scripts/install.sh`.

**Tech Stack:** Go, Cobra, standard library HTTP/archive/file APIs, existing repo test patterns

---

### Task 1: Fix `auth --login` post-import auth state

**Files:**
- Modify: `internal/cli/auth.go`
- Modify: `internal/cli/root_test.go`

- [ ] **Step 1: Write the failing test for restoring the previously active profile**

Add a test that:
- seeds `config.toml` with an `active_profile`
- simulates `auth --login` when no restorable on-disk auth should remain after import
- verifies the saved active profile is rewritten back to `auth.json`

- [ ] **Step 2: Run the targeted test to verify it fails**

Run: `env GOCACHE=/tmp/go-build-auth-update go test ./internal/cli -run TestAuthCommandLoginRestoresActiveProfileAfterImport`
Expected: FAIL because `auth.json` is missing or contains the wrong profile after import

- [ ] **Step 3: Implement the minimal active-profile restoration logic**

Update `auth --login` so it:
- records pre-import `active_profile`
- saves the imported profile
- restores the pre-import active profile to `deps.AuthPath` when it still exists
- otherwise promotes the new profile to active and writes it to `deps.AuthPath`

- [ ] **Step 4: Re-run the targeted test to verify it passes**

Run: `env GOCACHE=/tmp/go-build-auth-update go test ./internal/cli -run TestAuthCommandLoginRestoresActiveProfileAfterImport`
Expected: PASS

### Task 2: Print a manual login URL during `auth --login`

**Files:**
- Modify: `internal/cli/auth.go`
- Modify: `internal/cli/root_test.go`

- [ ] **Step 1: Write the failing test for login URL output**

Add a test that simulates `codex login` emitting a browser URL and verifies `auth --login` prints a manual-open hint.

- [ ] **Step 2: Run the targeted test to verify it fails**

Run: `env GOCACHE=/tmp/go-build-auth-update go test ./internal/cli -run TestAuthCommandLoginPrintsManualBrowserURL`
Expected: FAIL because no URL hint is currently printed

- [ ] **Step 3: Implement the minimal login output capture**

Adjust the auth runner/login path to:
- stream `codex login` output to the terminal
- capture the first `http://` or `https://` URL
- print a manual-open line when a URL is found

- [ ] **Step 4: Re-run the targeted test to verify it passes**

Run: `env GOCACHE=/tmp/go-build-auth-update go test ./internal/cli -run TestAuthCommandLoginPrintsManualBrowserURL`
Expected: PASS

### Task 3: Add the `update` command

**Files:**
- Modify: `internal/cli/root.go`
- Create: `internal/cli/update.go`
- Modify: `internal/cli/root_test.go`

- [ ] **Step 1: Write the failing tests for command registration and update behavior**

Add tests that verify:
- root command includes `update`
- updater chooses the correct asset name
- updater can replace a test binary from a release archive and refresh completions

- [ ] **Step 2: Run the targeted tests to verify they fail**

Run: `env GOCACHE=/tmp/go-build-auth-update go test ./internal/cli -run 'TestRootCommandIncludesCoreSubcommands|TestUpdateCommand.*'`
Expected: FAIL because `update` does not exist yet

- [ ] **Step 3: Implement the minimal native updater**

Implement `codex-switch update` to:
- detect current executable path and platform
- download the latest release asset
- extract `codex-switch`
- atomically replace the current executable
- regenerate zsh/bash/fish completions

- [ ] **Step 4: Re-run the targeted tests to verify they pass**

Run: `env GOCACHE=/tmp/go-build-auth-update go test ./internal/cli -run 'TestRootCommandIncludesCoreSubcommands|TestUpdateCommand.*'`
Expected: PASS

### Task 4: Verify end-to-end and prepare release

**Files:**
- Modify: `README.md`
- Verify: `internal/cli/auth.go`
- Verify: `internal/cli/update.go`
- Verify: `internal/cli/root_test.go`

- [ ] **Step 1: Update docs for login fallback and upgrade flow**

Document:
- `auth --login` manual browser link behavior
- `codex-switch update` as the preferred upgrade path for installed users

- [ ] **Step 2: Run full test suite**

Run: `env GOCACHE=/tmp/go-build-auth-update go test ./...`
Expected: PASS

- [ ] **Step 3: Review diff and commit**

Run:
- `git diff --stat`
- `git add <changed-files>`
- `git commit -m "feat(cli): add login fallback output and update command"`

- [ ] **Step 4: Push, merge, and release**

Run:
- `git push -u origin auth-login-update`
- create/merge PR
- tag next patch release
- push tag to trigger release workflow
