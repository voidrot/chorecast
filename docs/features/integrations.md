# Integrations API & timed entitlements

Status: accepted (planning session, 2026-10-30). **Ships after v1**, but the token and scope model is built into
the auth middleware from day one so no API redesign is needed later.

Motivating example: a script on a Windows PC unlocks a kid's account while they have "PC gaming" time and locks it
when the time runs out.

## Decisions

| Topic | Decision |
|---|---|
| Auth | **Scoped API keys**: named, explicit scopes, optional kid restriction, optional expiry, last-used tracking, instant revoke. |
| Timed rewards | **Both**: rewards can grant **timed entitlements** that kids bank and use, *and* a suitably scoped key can **redeem rewards on a kid's behalf**. |
| Starting time | **Kid starts it in the app** or an **integration starts it** (e.g. on PC login). Never automatic on approval. |
| Pausing | **Pause/resume by kid or integration**; parents can pause or end any session. Remaining minutes stay banked. |
| Expiry | **Optional per-reward expiry** of banked time, **default never**. |
| Time windows | **Optional per-entitlement allowed windows** (e.g. 15:00–20:00 school days). Starting outside is refused; a running session ends at window close and the remainder stays banked. |
| Change delivery | **REST polling** and **MQTT** (publish state to a broker for Home Assistant etc.). The existing webhooks also carry entitlement events. |
| Tooling | **Published API docs + Integrations guide** and a **CLI client** (`chorecast api ...`). |

## API keys

```
api_keys(id, name, prefix, token_hash, scopes JSON, kid_ids JSON?, created_by, created_at,
         expires_at?, last_used_at?, last_used_ip?, revoked_at?)
```

- Created in the web admin (Settings → Integrations) by **owners** (parents may create keys limited to
  read scopes; household setting). The full key is shown once: `cc_<prefix>_<secret>`; only the hash is stored.
- Sent as `Authorization: Bearer cc_...`. Rate-limited per key. Every write made with a key is audit-logged with
  the key name as the actor.
- **Kid restriction**: if `kid_ids` is set, every kid-scoped endpoint rejects other kids.

### Scopes

| Scope | Allows |
|---|---|
| `household:read` | kids list (id, display name, avatar), household settings needed by clients |
| `points:read` | balances and ledger |
| `chores:read` | instances, statuses, schedules |
| `entitlements:read` | banked minutes, active sessions, windows |
| `entitlements:control` | start, pause, resume, end sessions |
| `rewards:read` | reward catalog and redemptions |
| `rewards:redeem` | create redemptions on a kid's behalf (normal approval rules apply, see below) |
| `behavior:write` | log behavior entries (e.g. from a smart button) |
| `chores:complete` | mark instances done (e.g. a sensor confirms the dishwasher ran); evidence rules still apply |

**Day-one requirement:** every API operation declares its required scope(s) in Huma metadata. Parent sessions hold
all parent scopes, kid sessions hold kid scopes, display tokens hold read-only kid-visible scopes, and API keys
hold what was granted. One middleware enforces all of them.

## Timed entitlements

```
entitlement_types(id, key, name, icon, windows JSON?, archived)        -- key e.g. "pc-gaming"
entitlement_grants(id, kid_id, type_id, minutes, remaining_minutes, source_redemption_id?, created_at, expires_at?)
entitlement_sessions(id, kid_id, type_id, started_by: kid|integration|parent, started_via_key_id?,
                     started_at, paused_at?, ended_at?, end_reason?, consumed_seconds, heartbeat_at?)
```

- A **reward** may grant `{type: "pc-gaming", minutes: 30, expires_after_days: null}`. On redemption approval the
  points are deducted and a **grant** is banked. Parents can also add grants manually (audit-logged).
- **Available** = sum of unexpired `remaining_minutes` for that kid and type.
- **One active session per kid per type.** Starting takes an optional `minutes` cap (default: all available).
  Time is consumed from the earliest-expiring grants first.
- **Ending**: when available time hits zero, at window close, on pause/end, or by a parent.
  `end_reason`: `exhausted | window_closed | paused | ended_by_kid | ended_by_parent | ended_by_integration | heartbeat_lost`.
- **Heartbeat (integration-started sessions):** the integration sends `POST .../heartbeat` at least every 60 s.
  If heartbeats stop for 2 minutes (PC crashed or was unplugged), the session auto-pauses so the kid doesn't lose time.
  Kid-started sessions in the app have no heartbeat requirement.
- Expiring banked time: a daily job zeroes expired grants. Kids are notified 1 day before expiry (`points` category).
- The server is the clock. Integrations ask for `ends_at` / `remaining_seconds` rather than timing locally.

### Endpoints (sketch)

```
GET  /api/v1/kids/{kid}/entitlements                 → [{type, available_minutes, window_open, active_session}]
GET  /api/v1/kids/{kid}/entitlements/{type}          → {available_minutes, window: {open, closes_at}, active_session: {id, started_at, ends_at, remaining_seconds}}
POST /api/v1/kids/{kid}/entitlements/{type}/start    {minutes?}  → session   (409 if none available, 403 outside window)
POST /api/v1/kids/{kid}/entitlements/{type}/pause
POST /api/v1/kids/{kid}/entitlements/{type}/heartbeat
POST /api/v1/kids/{kid}/entitlements/{type}/end
```

`{kid}` accepts the kid's id or a stable slug so scripts read naturally.

## Redeeming via API

- A key with `rewards:redeem` creates a redemption exactly as the kid would: points held, then parent approval.
- A reward can be marked **`auto_approve_from_integrations`** (off by default). Then a key-initiated redemption is
  approved immediately. This enables "spend 10 points for 15 minutes" flows at the PC without a parent in the loop.
  This is the one exception to "rewards need parent approval" in [points.md](points.md), and it's opt-in per reward.
- Redemption limits, stock, cooldowns and per-kid availability still apply.

## MQTT

- Optional, configured by env: `CHORECAST_MQTT_URL` (`mqtt://` or `mqtts://`), `_USERNAME`, `_PASSWORD`,
  `_TOPIC_PREFIX` (default `chorecast`).
- **Publish-only** (commands go through REST): retained JSON state topics, updated on change.
  ```
  chorecast/kids/<slug>/points                     {"balance": 420, "available": 380}
  chorecast/kids/<slug>/entitlements/<type>        {"available_minutes": 45, "active": true, "ends_at": "...", "window_open": true}
  chorecast/kids/<slug>/chores                     {"open": 2, "submitted": 1, "done": 5, "missed": 0}
  chorecast/household/pending_reviews              {"count": 3}
  ```
- **Home Assistant MQTT discovery** messages (optional flag) create sensors and binary sensors automatically.
- Only kid slugs and counts are published, no photos or notes. The admin UI notes that the broker receives this data.

## CLI client

`chorecast api` subcommands in the same binary, configured by `CHORECAST_API_URL` and `CHORECAST_API_KEY`:

```
chorecast api kids
chorecast api points --kid sam
chorecast api entitlement status --kid sam --type pc-gaming [--json]
chorecast api entitlement start  --kid sam --type pc-gaming [--minutes 30]
chorecast api entitlement heartbeat --kid sam --type pc-gaming
chorecast api entitlement pause  --kid sam --type pc-gaming
chorecast api redeem --kid sam --reward "15 min gaming"
```

Exit codes are script-friendly (e.g. `entitlement status` exits 0 if active, 3 if none available, 4 if outside the
window). The client is generated from `api/openapi.json` with oapi-codegen's client mode, so it stays in sync.
Release binaries are published for Windows, macOS and Linux (amd64/arm64) alongside the server.

## Documentation

- The OpenAPI spec and interactive docs are already served at `/api/v1/openapi.json` and `/api/v1/docs`.
- `docs/integrations.md`: creating keys and scopes, the entitlement lifecycle, a worked **Windows PC lock/unlock**
  walkthrough (Task Scheduler + CLI, logon start, heartbeat loop, lock on end), MQTT and Home Assistant setup.

## Phasing

- **v1 (day one):** scope-aware auth middleware, `api` token kind reserved, kid slugs.
- **Post-v1:** API keys UI, entitlement types/grants/sessions, timed rewards, MQTT, CLI, Integrations guide.

## Open questions

- None outstanding.
