# codex-switch: Event-Driven Watch Redesign

## Problem

The current `watch` design uses fixed-interval quota probes. That has two issues:

- It spends quota even while Codex is idle.
- It still reacts slowly if the interval is too large.

The redesign should reduce unnecessary probes without losing timely switching when the active account is close to depletion.

## Decision Summary

Replace periodic watch polling with an event-driven design:

- On `watch` startup, run exactly one calibration probe for the current `active_profile`.
- After startup, do not run periodic quota polling.
- Watch local Codex session event files under `~/.codex/sessions/**/*.jsonl`.
- Use local `token_count` events as low-cost trigger signals.
- When a trigger indicates threshold pressure, confirm the active profile with a direct quota probe.
- Only after confirmation, probe candidate profiles and switch if a better profile exists.

This design keeps probe cost near zero while idle, reacts during real usage, and avoids trusting stale or mismatched local session state as the source of truth.

## Why Session Events Are Not Enough

Local session files expose useful signals:

- `event_msg.payload.type == "user_message"`
- `event_msg.payload.type == "token_count"`
- `event_msg.payload.rate_limits.primary.used_percent`
- `event_msg.payload.rate_limits.secondary.used_percent`

Observed `token_count` shape:

```json
{
  "timestamp": "2026-03-23T09:03:31.174Z",
  "type": "event_msg",
  "payload": {
    "type": "token_count",
    "rate_limits": {
      "limit_id": "codex",
      "primary": {
        "used_percent": 18.0,
        "window_minutes": 300,
        "resets_at": 1774271142
      },
      "secondary": {
        "used_percent": 35.0,
        "window_minutes": 10080,
        "resets_at": 1774835971
      },
      "credits": null,
      "plan_type": "team"
    },
    "info": {
      "last_token_usage": {
        "total_tokens": 101431
      }
    }
  }
}
```

However, local session `rate_limits` cannot be treated as authoritative for `active_profile`.

Real observation from this machine:

- Session event sample reported `plan_type=team`, `primary=18`, `secondary=35`
- Direct probe of current `~/.codex/auth.json` reported `plan_type=plus`, `primary=13`, `secondary=90`

That mismatch means the running local session may reflect older credentials or a different account state than the auth file that `codex-switch` controls. Session events are therefore only triggers, not final truth.

## Confirmed Probe Contract

The direct quota check remains:

```text
POST https://chatgpt.com/backend-api/codex/responses
```

Request body:

```json
{
  "model": "gpt-5.4-mini",
  "input": [{"role": "user", "content": "hi"}],
  "instructions": ".",
  "store": false,
  "stream": true,
  "reasoning": {"effort": "none"}
}
```

Confirmed response characteristics:

- HTTP status: `200`
- Body: SSE event stream, not plain JSON
- Quota data lives in `x-codex-*` response headers

Observed headers:

```json
{
  "x-codex-plan-type": "plus",
  "x-codex-primary-used-percent": "13",
  "x-codex-secondary-used-percent": "90",
  "x-codex-primary-reset-after-seconds": "12469",
  "x-codex-secondary-reset-after-seconds": "179937",
  "x-codex-primary-reset-at": "1774269073",
  "x-codex-secondary-reset-at": "1774436541",
  "x-codex-credits-has-credits": "False"
}
```

Because the quota signal is already stable in the response headers, `watch` should keep using direct HTTP probes rather than shelling out to `codex exec`.

## Why Not Use `codex exec`

`codex exec` is the wrong primitive for watch confirmation:

- It starts a full Codex CLI session.
- It loads local instructions, skills, sandbox settings, and repo context.
- Default output is human-oriented transcript text.
- `--json` output is an agent event stream, not a dedicated quota API.
- It is significantly heavier than a direct probe.

`watch` should not depend on CLI transcript parsing for quota decisions.

## Architecture

### Runtime Model

`watch` becomes a foreground event loop with three responsibilities:

1. Startup calibration
2. Session event ingestion
3. Confirm-and-switch workflow

### Startup Calibration

When `codex-switch watch` starts:

1. Load config and ensure `active_profile` is set.
2. Run one direct quota probe for `active_profile`.
3. Persist the calibration snapshot.
4. If thresholds are already exceeded, immediately enter candidate selection.
5. Begin watching local session event files.

There is no repeating ticker after startup.

### Session Event Ingestion

The watcher tails files under:

```text
~/.codex/sessions/**/*.jsonl
```

Supported events:

- `user_message`
  - Optional activity signal only
- `token_count`
  - Primary trigger event

The implementation must support:

- existing session files continuing to grow
- new session files appearing while watch is running
- process restart with resume-from-tail behavior
- malformed JSON lines without crashing the watcher

### Trigger Logic

`token_count` is the only network-triggering event.

When a `token_count` event arrives:

1. Parse `payload.rate_limits.primary.used_percent`
2. Parse `payload.rate_limits.secondary.used_percent`
3. Compare against configured thresholds
4. If neither threshold is exceeded, do nothing
5. If either threshold is exceeded, trigger active-profile confirmation

Example default thresholds:

- primary `>= 90`
- secondary `>= 95`

### Active-Profile Confirmation

On threshold trigger:

1. Load the current `active_profile` from `config.toml`
2. Probe that profile directly via HTTP
3. If the confirmed snapshot is now below thresholds, do nothing else
4. If the confirmed snapshot still exceeds thresholds, start candidate evaluation

This confirmation step is required because local session event state may not correspond to the auth file currently managed by `codex-switch`.

### Candidate Evaluation

Candidate evaluation runs only after confirmed threshold pressure on the active profile.

Workflow:

1. List all saved profiles except the current active one
2. Probe candidates one by one
3. Rank by:
   - lower `secondary_used_percent`
   - then lower `primary_used_percent`
   - then lexical name tie-break
4. Only switch if the best candidate has strictly lower weekly usage than the confirmed active profile
5. If no better candidate exists, log and optionally notify that no better profile is available

This preserves the existing anti-ping-pong behavior.

### Post-Switch Cooldown

After a successful switch, ignore new threshold triggers for a short cooldown window.

Recommended default:

- `60s`

Purpose:

- avoid repeated candidate probes caused by bursty session events
- avoid immediate duplicate switching attempts while the local session catches up

During cooldown, session events may still be recorded, but they do not trigger new probes.

## Data Storage

### `~/.codex-switch/watch-state.toml`

Stores the current watch runtime state and recent confirmed snapshots.

Suggested structure:

```toml
last_cleanup_at = 2026-03-23T09:00:00Z

[runtime]
active_profile = "personal"
cooldown_until = 2026-03-23T09:04:31Z

[profiles.personal]
last_confirmed_at = 2026-03-23T09:03:31Z
last_triggered_at = 2026-03-23T09:03:30Z
last_trigger_source = "session_rate_limits"
last_plan = "plus"
last_primary_used_percent = 13
last_secondary_used_percent = 90
last_primary_reset_at = 2026-03-23T12:17:53Z
last_secondary_reset_at = 2026-03-25T11:42:21Z

[[profiles.personal.samples]]
at = 2026-03-23T08:00:00Z
primary_used_percent = 4
secondary_used_percent = 82

[[profiles.personal.samples]]
at = 2026-03-23T09:03:31Z
primary_used_percent = 13
secondary_used_percent = 90
```

Keep only the most recent 5 confirmed samples per profile.

### `~/.codex-switch/watch-checks.jsonl`

Append-only event log for confirmed probes and switching outcomes.

Each line should contain:

```json
{
  "at": "2026-03-23T09:03:31Z",
  "profile": "personal",
  "kind": "active_check",
  "trigger": "session_rate_limits",
  "success": true,
  "plan_type": "plus",
  "primary_used_percent": 13,
  "secondary_used_percent": 90,
  "estimated_tokens": 19,
  "estimated_cost_usd": "0.0000",
  "switched_to": null
}
```

`kind` values:

- `startup_calibration`
- `active_check`
- `candidate_check`
- `switch`

### `~/.codex-switch/watch.log`

Human-readable operational log for:

- startup
- file watch registration
- session parsing warnings
- threshold triggers
- confirmation decisions
- switch decisions
- retention cleanup results

## Retention Policy

Both `watch-checks.jsonl` and `watch.log` must be retained for 7 days only.

Requirements:

- On watch startup, remove entries older than 7 days.
- During runtime, perform lightweight cleanup at most once per day.
- Cleanup must rewrite files atomically to avoid corruption.

This keeps rolling 12h, 24h, and 7d statistics accurate while preventing unbounded file growth.

## Statistics

Statistics are derived from `watch-checks.jsonl`, never from mutable counters.

Supported windows:

- past 12 hours
- past 24 hours
- past 7 days

For each window, compute:

- total probe count
- startup calibration count
- active check count
- candidate check count
- switch count
- success count
- failure count
- estimated tokens used by watch probes
- estimated cost of watch probes

Important: these numbers describe watcher probe cost, not all Codex usage.

The local session `token_count` event may expose large `last_token_usage` values, but those belong to actual user requests and should not be mixed into watch probe accounting.

## Error Handling

### Session File Errors

- malformed JSON line: skip line, log warning
- missing fields: skip event, log debug
- file deleted or rotated: drop reader and re-register
- no session files present: continue waiting for file creation

### Probe Errors

- network error: log probe failure, no switch
- retryable 5xx / challenge responses: use existing retry behavior
- 429: treat as 100% used as already implemented
- 401: follow the existing token refresh path if refresh support is wired into watch later

### State Consistency

- always reload `active_profile` from config before confirmation
- always update the in-memory active profile after a successful switch
- never continue comparing against the startup-time active profile after switching

## CLI Behavior

Startup output should make the mode explicit:

```text
watching local Codex session events
startup calibration complete for personal: primary 13%, weekly 90%
threshold triggers will confirm via direct quota probe
```

Runtime output examples:

```text
session trigger exceeded threshold; confirming active profile personal
confirmed personal is over threshold; evaluating 2 candidate profiles
switching from personal to work
```

## Testing Plan

### Unit Tests

- parse valid `token_count` event with `payload.rate_limits`
- ignore `token_count` event with missing `rate_limits`
- ignore malformed JSON lines
- trigger confirmation only when threshold is exceeded
- enforce cooldown after switch
- cleanup removes entries older than 7 days

### Integration-Style Tests

- startup calibration runs exactly once
- session trigger above threshold causes active confirmation probe
- active confirmation below threshold prevents candidate probing
- active confirmation above threshold probes candidates and switches to best option
- active profile is updated after switch for subsequent decisions
- new session files created after startup are detected

## Non-Goals

- cross-machine coordination
- detecting quota consumption from other machines in real time
- using `codex exec` as a quota-check transport
- estimating exact billed token usage for watch probes from server truth

## Implementation Notes

This redesign changes the role of `watch` from "periodic poller" to "local event monitor with targeted confirmation". The local session layer is cheap and immediate. The direct HTTP probe remains the authoritative source when making switching decisions.
