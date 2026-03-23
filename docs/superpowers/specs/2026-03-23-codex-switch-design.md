# codex-switch: Multi-Account Codex CLI Manager

## Problem

Users with multiple ChatGPT Plus accounts for Codex CLI face friction when switching between accounts:
- No visibility into each account's remaining quota
- Must manually `codex logout` then `codex login` to switch
- No way to compare quotas across accounts
- No automatic switching when one account is depleted

## Solution

A Go CLI tool (`codex-switch`) that manages multiple Codex account profiles, queries quota via API response headers, and optionally auto-switches when an account's quota is low.

## Architecture

### Data Storage

```
~/.codex-switch/
├── config.toml          # Global configuration
└── profiles/
    ├── <name>.json      # Auth credentials (same format as ~/.codex/auth.json)
    └── ...
```

**Profile file format** (identical to `~/.codex/auth.json`):

```json
{
  "auth_mode": "chatgpt",
  "OPENAI_API_KEY": null,
  "tokens": {
    "id_token": "...",
    "access_token": "...",
    "refresh_token": "...",
    "account_id": "..."
  },
  "last_refresh": "2026-03-22T17:03:55.366292Z"
}
```

**config.toml**:

```toml
# Model used for quota check requests (use cheapest available)
check_model = "gpt-5.4-mini"

# Active profile name (updated on switch)
active_profile = ""

# Auto-check quota when listing (can be disabled)
auto_check = false

# Watch settings (watch is off by default, must be explicitly started)
[watch]
interval_minutes = 5
primary_threshold_percent = 90
secondary_threshold_percent = 95
notify = true  # Desktop notifications on auto-switch
```

### Commands

#### `codex-switch auth <name>`

Registers a new account profile by driving the OAuth login flow.

1. If `~/.codex/auth.json` exists, back it up to `~/.codex/auth.json.bak`
2. Run `codex logout` to clear current session
3. Run `codex login` — opens browser for OAuth
4. Wait for login to complete
5. Copy the resulting `~/.codex/auth.json` to `~/.codex-switch/profiles/<name>.json`
6. Restore `~/.codex/auth.json.bak` back to `~/.codex/auth.json` (so the user's current session is not disrupted)
7. If this is the first profile, set it as `active_profile`

**Edge cases:**
- If `<name>` already exists, prompt to overwrite
- If login fails or is cancelled, restore backup and report error
- **Warning:** Users should not run this while a Codex CLI session is active, as `codex logout` will invalidate the current session temporarily. Print a warning before proceeding.
- If backup restore fails (disk error), print the backup path so user can manually recover

#### `codex-switch list` / `codex-switch ls`

Displays all profiles with their quota status in a table.

By default, queries the API for the latest quota of each profile. Use `--no-check` to skip the query and show only cached/stored info.

```
NAME        PLAN   5H USED   WEEKLY USED   5H RESET    WEEKLY RESET   ACTIVE
account1    plus   2%        86%           4h 20m      2d 9h          *
account2    plus   45%       30%           2h 10m      5d 3h
account3    plus   0%        12%           5h 0m       6d 22h
```

**Sorting:** By default sorted by weekly usage (ascending), so the account with the most remaining quota is at the top.

**Empty state:** If no profiles exist, print: `No profiles found. Run 'codex-switch auth <name>' to add one.`

#### `codex-switch use <name>`

Switches to the specified account.

1. Copy `~/.codex-switch/profiles/<name>.json` to `~/.codex/auth.json` using atomic write (write to temp file, then `os.Rename`)
2. Update `active_profile` in config.toml
3. Print confirmation with the account's current quota

No need to restart Codex CLI — the next API call will use the new credentials.

**Note:** All writes to `~/.codex/auth.json` (in `use`, `watch`, and token refresh) use atomic write (write to a temp file in the same directory, then rename) to prevent partial reads by Codex CLI.

#### `codex-switch status`

Shows detailed quota info for the currently active account.

```
Active: account1 (plus)

5-hour window:
  Used: 2%
  Resets in: 4h 20m (at 2026-03-23 14:31)

Weekly quota:
  Used: 86%
  Resets in: 2d 9h (at 2026-03-25 18:42)

Credits: none
```

#### `codex-switch watch`

Starts a foreground process that periodically checks the active account's quota and auto-switches when thresholds are exceeded.

**Logic (each interval):**

```
1. Query current account's quota via API
2. If primary_used >= primary_threshold OR secondary_used >= secondary_threshold:
   a. Query all other profiles' quotas (in parallel)
   b. Sort by secondary_used ascending (prefer the account with most weekly quota remaining)
   c. Only switch if the best candidate has secondary_used < current account's secondary_used (prevent ping-pong)
   d. If a better account is found:
      - Atomic-write its auth.json to ~/.codex/auth.json
      - Update active_profile in config
      - Send desktop notification: "Switched to <name> (weekly: X% used)"
      - Log the switch event
   e. If no better account is available:
      - Send notification: "All accounts depleted"
3. Sleep for interval_minutes
```

**Running as daemon:** Users can background it with `codex-switch watch &` or use their own process manager. The tool itself runs in the foreground with log output.

#### `codex-switch remove <name>`

Removes a profile. Refuses to remove the active profile without `--force`.

### Quota Query Mechanism

**API call:**

```
POST https://chatgpt.com/backend-api/codex/responses
Headers:
  Authorization: Bearer <access_token>
  ChatGPT-Account-Id: <account_id>
  Content-Type: application/json
Body:
  {
    "model": "<check_model from config>",
    "input": [{"role": "user", "content": "hi"}],
    "instructions": ".",
    "store": false,
    "reasoning": {"effort": "none"}
  }
```

**Response headers extracted:**

| Header | Description |
|--------|-------------|
| `x-codex-plan-type` | Plan type (plus, pro, etc.) |
| `x-codex-primary-used-percent` | 5h window usage % |
| `x-codex-secondary-used-percent` | Weekly usage % |
| `x-codex-primary-reset-after-seconds` | Seconds until 5h window resets |
| `x-codex-secondary-reset-after-seconds` | Seconds until weekly quota resets |
| `x-codex-primary-reset-at` | Unix timestamp of 5h reset |
| `x-codex-secondary-reset-at` | Unix timestamp of weekly reset |
| `x-codex-credits-has-credits` | Whether user has purchased credits |
| `x-codex-credits-balance` | Remaining credit balance |

**Cost:** Each check uses ~19 tokens on the configured model. With `gpt-5.4-mini`, this is negligible. Note: changing `check_model` to a more expensive model will increase per-check cost proportionally.

**Model configurability:** The `check_model` field in config.toml lets users change the model used for checks. If a model is deprecated, users update this field. The tool does not hardcode any model name — it reads from config.

### Token Refresh

OAuth access tokens expire. When a quota check returns 401:

1. Use the `refresh_token` to get a new access token via:
   ```
   POST https://auth.openai.com/oauth/token
   Content-Type: application/x-www-form-urlencoded
   grant_type=refresh_token&refresh_token=<refresh_token>&client_id=app_EMoamEEZ73f0CkXaXp7hrann
   ```
   The `client_id` is the Codex CLI's OAuth app ID (extracted from JWT `aud` claim). This is a public client — no `client_secret` is needed.
   The response returns: `access_token`, `id_token`, `refresh_token`, `expires_in`, `scope`, `token_type`.
2. Update the profile's `access_token`, `id_token`, `refresh_token`, and `last_refresh` fields
3. Also update `~/.codex/auth.json` if this is the active profile
4. Retry the quota check

If refresh fails, add `"status": "expired"` to the profile JSON and skip it in auto-switch. `codex-switch list` shows expired profiles with a warning icon. Users must re-auth with `codex-switch auth <name>` to fix.

### Error Handling

- **Network errors:** Retry up to 3 times with backoff, then skip
- **401 Unauthorized:** Attempt token refresh; if that fails, mark profile expired
- **429 Rate Limited:** The account is already limited — this is useful data, mark it as 100% used
- **Cloudflare challenge:** Retry with backoff; if persistent, report error

### Project Structure

```
codex-switch/
├── cmd/
│   └── codex-switch/
│       └── main.go          # CLI entry point (cobra)
├── internal/
│   ├── config/
│   │   └── config.go        # Config loading/saving
│   ├── profile/
│   │   └── profile.go       # Profile CRUD operations
│   ├── quota/
│   │   └── quota.go         # API quota checking
│   ├── auth/
│   │   └── refresh.go       # OAuth token refresh
│   ├── switcher/
│   │   └── switcher.go      # Account switching logic
│   └── watcher/
│       └── watcher.go       # Background watch loop
├── go.mod
├── go.sum
└── docs/
```

### Dependencies

- `github.com/spf13/cobra` — CLI framework
- `github.com/pelletier/go-toml/v2` — TOML config
- `github.com/fatih/color` — Terminal colors for table output
- `github.com/gen2brain/beeep` — Desktop notifications (cross-platform)

### Non-Goals

- Does not proxy or wrap Codex CLI process
- Does not modify Codex CLI's internal behavior
- Does not support API key auth mode (only ChatGPT OAuth)
- Does not track detailed token usage history
