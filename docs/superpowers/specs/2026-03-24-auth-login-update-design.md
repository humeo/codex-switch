# codex-switch: Auth Import UX and Self-Update

## Problem

Two usability gaps and one auth-state bug are currently visible in the CLI:

1. `codex-switch auth --login` relies on `codex login` opening a browser automatically, but does not surface a manual login URL when the browser launch fails.
2. `codex-switch` has no built-in update command, so installed users must re-run the external install script manually.
3. `codex-switch auth --login` can leave Codex CLI effectively signed out even though `codex-switch list` still shows an `active_profile`.

The third issue is caused by a mismatch between configuration state and real auth-file state:

- `list` marks a row active only from `config.toml.active_profile`
- `auth --login` temporarily moves `~/.codex/auth.json` aside, runs `codex logout` and `codex login`, then restores or removes `auth.json`
- if the machine had no restorable active auth file at the end of import, Codex can still start in a signed-out state until the user runs `codex-switch use <name>`

This produces the confusing flow the user reported:

1. import a new account with `auth --login`
2. run `codex-switch list` and still see the old account marked active
3. run `codex` and get the sign-in screen
4. run `codex-switch use <old-active-profile>` and `codex` works again

## Goals

- Make `auth --login` surface a manual browser-login URL when Codex emits one.
- Ensure `auth --login` never leaves the machine in a misleading "active in config, signed out in practice" state.
- Add a first-party `codex-switch update` command for release-installed users.
- Preserve current profile storage, quota probing, and switch semantics.

## Non-Goals

- Do not implement a custom OAuth flow.
- Do not add `update --version` in this change.
- Do not support updating arbitrary source-built binaries.
- Do not redesign how `list` determines the active row.
- Do not change release publication workflow in this change.

## Decision Summary

### 1. `auth --login` should restore a usable active auth state

Treat `auth --login` as an account import flow, not a "leave Codex logged out" flow.

After import succeeds:

- if the pre-import `active_profile` still exists in the profile store, rewrite that profile back to `~/.codex/auth.json`
- otherwise, make the newly imported profile active and write it to `~/.codex/auth.json`

This keeps `config.toml.active_profile` and the actual on-disk auth state aligned.

### 2. `auth --login` should surface a manual login link

When running `codex login`:

- forward the child process output to the terminal
- scan stdout/stderr text for the first `http://` or `https://` URL
- if one is found, print a clear fallback line telling the user to open that link manually if the browser did not launch

If no URL is found, the flow still succeeds without extra output.

### 3. Add `codex-switch update`

Add a native Go command:

```text
codex-switch update
```

The command is intentionally narrow:

- only supports release-installed users
- always updates to the latest GitHub release
- replaces the current executable in place
- refreshes shell completions after replacing the binary

## User-Facing Behavior

### `auth --login`

Current warning remains:

```text
warning: codex logout and codex login will run now
```

If a login URL is detected, print:

```text
If the browser did not open automatically, open this link manually: <url>
```

After the profile is imported:

- Codex should still have a usable auth file on disk
- if there was an existing active profile, that profile remains the active Codex session
- if there was no recoverable active profile, the newly imported account becomes active

### `update`

Example:

```text
$ codex-switch update
checking latest release for humeo/codex-switch
downloading codex-switch_darwin_arm64.tar.gz
replaced /Users/example/.local/bin/codex-switch
refreshed zsh completion
refreshed bash completion
refreshed fish completion
updated codex-switch successfully
```

If the executable path is not writable:

```text
update failed: current executable is not writable: <path>
```

If completions cannot be refreshed, the binary update still succeeds and prints warnings.

## Detailed Design

### Auth Import Flow

#### Current behavior

`captureAuthViaLogin()` in [internal/cli/auth.go](/Users/koltenluca/code-github/codex-switch/internal/cli/auth.go):

1. renames `auth.json` to `auth.json.bak` if it exists
2. runs `codex logout`
3. runs `codex login`
4. reads the newly written `auth.json`
5. restores or removes `auth.json` in a deferred cleanup

The import command then saves the new profile and only updates `active_profile` when the imported account is the first saved profile.

That separation is what allows config state and actual Codex auth state to diverge.

#### New behavior

Split the auth-import logic into two responsibilities:

1. capture a newly logged-in auth payload
2. restore the post-import active auth file intentionally

Implementation shape:

- load config before starting the login flow
- record the pre-import `active_profile`
- capture the imported auth payload with the existing backup/restore discipline
- save the imported profile
- compute a `postImportActiveProfile`
- write that profile to `deps.AuthPath`
- persist `cfg.ActiveProfile` if the post-import active profile changed

#### Post-import active-profile selection

Rules:

1. If `cfg.ActiveProfile` is non-empty and that profile exists in the store after import, keep it active.
2. Otherwise, set the newly imported profile as active.

This handles all relevant cases:

- importing a second account while another account is active
- importing the first ever account
- importing while config points at a deleted or missing profile
- importing over the currently active profile

### Login URL Extraction

`authRunnerFactory` currently exposes only:

```go
Run(ctx, name, args...)
```

That is enough for fire-and-forget commands such as `codex logout`, but not enough for URL extraction.

Refine the auth execution abstraction so the login path can:

- stream child-process output
- inspect it line-by-line
- still fail with the child process exit status if login fails

Recommended structure:

- keep the existing `Runner` behavior for simple commands
- add a login-specific helper that runs `codex login` with stdout/stderr connected to writers
- use an `io.MultiWriter` or equivalent tee so output is both shown to the user and captured for URL parsing

URL matching can stay deliberately simple:

- regex for first `https?://` token
- first match wins

This is adequate because the goal is a fallback hint, not a structured OAuth parser.

### `update` Command

#### Command scope

Add `newUpdateCommand(deps)` and register it in `NewRootCommand()`.

The command will:

1. find the current executable path via `os.Executable()`
2. detect current OS/arch using Go runtime APIs
3. build the expected release asset name:
   - `codex-switch_darwin_amd64.tar.gz`
   - `codex-switch_darwin_arm64.tar.gz`
   - `codex-switch_linux_amd64.tar.gz`
   - `codex-switch_linux_arm64.tar.gz`
4. resolve the latest GitHub release download URL
5. download the archive to a temp directory
6. extract `codex-switch`
7. atomically replace the current executable
8. regenerate completions using the new binary

#### Release source

The update path targets:

```text
https://github.com/humeo/codex-switch/releases/latest/download/<asset>
```

No version selection is exposed in this change.

#### Atomic replacement

Binary replacement should follow the same safety pattern already used elsewhere in the repo:

- write to a temp file in the executable directory
- chmod appropriately
- rename over the current executable

If the executable directory is not writable, fail early with a clear message.

#### Completion refresh

Refresh the same completion locations currently managed by `scripts/install.sh`:

- `~/.zsh/completions/_codex-switch`
- `~/.local/share/bash-completion/completions/codex-switch`
- `~/.config/fish/completions/codex-switch.fish`

Behavior:

- completion refresh warnings do not roll back a successful binary update
- update should still report partial success clearly if some completions could not be regenerated

## Error Handling

### `auth --login`

- If `codex logout` fails, abort immediately.
- If `codex login` fails, abort and restore previous auth state exactly as today.
- If imported auth cannot be read from disk, abort and restore previous auth state.
- If imported profile save succeeds but post-import active-profile write fails, return an error and do not silently claim success.

### `update`

- Unsupported OS/arch: return a clear unsupported-platform error.
- Download failure: return error without touching the current binary.
- Archive missing expected binary: return error.
- Executable path not writable: return error.
- Completion refresh failure: warn and continue.

## Testing Plan

### Auth Tests

Add regression tests for:

1. `auth --login` restores the previously active profile to `auth.json` when one exists.
2. `auth --login` promotes the newly imported profile to active when no recoverable active profile exists.
3. `auth --login` prints the manual URL hint when login output includes a URL.
4. `auth --login` still succeeds normally when no URL is present.

### Update Tests

Add tests for:

1. root command includes `update`
2. updater chooses the correct asset name for current platform
3. updater replaces the current binary from a test release archive
4. updater refreshes completions
5. updater preserves the current install on download/extract failure

## Implementation Notes

- Reuse existing atomic-write patterns rather than introducing a second file-replacement style.
- Keep updater logic in Go, not by shelling out to `scripts/install.sh`.
- Keep auth bugfix, login-link output, and update command in one implementation batch because they all touch install/auth UX and share user-facing documentation updates.

## Rollout

After implementation:

- update README install section with an "Upgrade" example using `codex-switch update`
- mention that `auth --login` now prints a manual login link when available
- verify that importing an account no longer leaves Codex signed out unexpectedly
