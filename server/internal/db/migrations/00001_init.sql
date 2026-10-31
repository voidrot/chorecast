-- +goose Up
-- The household this server hosts. There is exactly one (id = 1); its existence means first-run
-- setup is complete (internal/setup).
CREATE TABLE household (
    id         INTEGER PRIMARY KEY CHECK (id = 1),
    name       TEXT    NOT NULL,
    timezone   TEXT    NOT NULL,                  -- IANA name
    day_end    TEXT    NOT NULL DEFAULT '00:00',  -- HH:MM; 00:00 = midnight at the end of the day
    week_start INTEGER NOT NULL DEFAULT 1 CHECK (week_start BETWEEN 0 AND 6), -- 0 = Sunday
    created_at TEXT    NOT NULL
);

-- First-run setup token (one row, SHA-256 only). Replaced on every start until setup completes,
-- which deletes it.
CREATE TABLE setup_token (
    id         INTEGER PRIMARY KEY CHECK (id = 1),
    token_hash BLOB    NOT NULL,
    created_at TEXT    NOT NULL
);

CREATE TABLE users (
    id            INTEGER PRIMARY KEY,
    display_name  TEXT    NOT NULL,
    role          TEXT    NOT NULL CHECK (role IN ('parent', 'child')),
    is_owner      INTEGER NOT NULL DEFAULT 0 CHECK (is_owner IN (0, 1) AND (is_owner = 0 OR role = 'parent')),
    slug          TEXT    UNIQUE,          -- stable short name usable in place of the id (kids); see internal/kids
    username      TEXT    UNIQUE,          -- parents only; kids sign in via paired device + PIN
    password_hash TEXT,                    -- argon2id, parents only
    pin_hash      TEXT,                    -- argon2id, kids (optional)
    pin_required  INTEGER NOT NULL DEFAULT 0 CHECK (pin_required IN (0, 1)), -- kids: PIN to open their profile
    color         TEXT,                    -- kids: #rrggbb accent
    birth_date    TEXT,                    -- kids: YYYY-MM-DD (optional)
    avatar        TEXT,                    -- kids: built-in avatar id (internal/kids.AvatarIDs)
    day_end_override TEXT,                 -- kids: HH:MM, at or before the household day end
    quiet_start   TEXT,                    -- kids: HH:MM; no notifications from quiet_start to quiet_end
    quiet_end     TEXT,                    --   (may wrap past midnight)
    school_days   INTEGER NOT NULL DEFAULT 0, -- kids: bitmask of weekdays, bit 0 = Sunday
    school_start  TEXT,                    -- kids: HH:MM, extra quiet window on school days
    school_end    TEXT,
    archived_at   TEXT,                    -- kids: archived (hidden, devices revoked, history kept)
    oidc_subject  TEXT    UNIQUE,          -- set when linked to an external IdP
    parent_pin_hash TEXT,                  -- argon2id, parents (optional): shared-tablet elevation
    totp_secret     TEXT,                  -- base32; set while enrolling, verified before totp_enabled
    totp_enabled    INTEGER NOT NULL DEFAULT 0 CHECK (totp_enabled IN (0, 1)),
    totp_last_step  INTEGER NOT NULL DEFAULT 0, -- last accepted TOTP time step; codes can't be replayed
    removed_at      TEXT,                  -- parents: removed (row kept so history still names them)
    created_at    TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

-- Single-use invite links for new parents (internal/parents). Only the SHA-256 of the token is stored.
CREATE TABLE invites (
    id         INTEGER PRIMARY KEY,
    token_hash BLOB    NOT NULL UNIQUE,
    role       TEXT    NOT NULL CHECK (role IN ('parent', 'owner')),
    created_by INTEGER REFERENCES users (id) ON DELETE SET NULL,
    created_at TEXT    NOT NULL,
    expires_at TEXT    NOT NULL,
    used_at    TEXT,
    used_by    INTEGER REFERENCES users (id) ON DELETE SET NULL
);

-- One-time 2FA recovery codes (SHA-256 of the normalized code). Replaced as a set.
CREATE TABLE recovery_codes (
    id         INTEGER PRIMARY KEY,
    user_id    INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    code_hash  BLOB    NOT NULL,
    used_at    TEXT,
    created_at TEXT    NOT NULL,
    UNIQUE (user_id, code_hash)
);

-- Opaque bearer/session tokens. Only the SHA-256 of the token is stored.
CREATE TABLE tokens (
    id           INTEGER PRIMARY KEY,
    token_hash   BLOB    NOT NULL UNIQUE,
    kind         TEXT    NOT NULL CHECK (kind IN ('web_session', 'app', 'device', 'display', 'api')),
    user_id      INTEGER REFERENCES users (id) ON DELETE CASCADE, -- NULL for shared-device tokens
    label        TEXT    NOT NULL DEFAULT '',
    created_at   TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    last_used_at TEXT,
    expires_at   TEXT,
    -- Sessions and app tokens always belong to a person; devices, displays and API keys may not.
    CHECK (user_id IS NOT NULL OR kind IN ('device', 'display', 'api'))
);

-- Background jobs (internal/jobs). Delivery is at-least-once: handlers must be idempotent.
CREATE TABLE jobs (
    id              INTEGER PRIMARY KEY,
    queue           TEXT    NOT NULL,
    kind            TEXT    NOT NULL,
    payload         TEXT    NOT NULL DEFAULT '{}',   -- JSON
    idempotency_key TEXT    UNIQUE,                  -- NULL = no dedup
    status          TEXT    NOT NULL DEFAULT 'queued' CHECK (status IN ('queued', 'running', 'done', 'failed')),
    run_at          TEXT    NOT NULL,
    attempts        INTEGER NOT NULL DEFAULT 0,
    max_attempts    INTEGER NOT NULL,
    last_error      TEXT,
    locked_at       TEXT,
    created_at      TEXT    NOT NULL,
    finished_at     TEXT
);
CREATE INDEX jobs_due ON jobs (queue, status, run_at);

-- Last occurrence each recurring schedule enqueued (internal/jobs scheduler).
CREATE TABLE schedule_cursors (
    name          TEXT PRIMARY KEY,
    last_fired_at TEXT NOT NULL
);

-- Stored responses for POSTs sent with an Idempotency-Key (internal/idempotency).
CREATE TABLE idempotency_keys (
    token_id     INTEGER NOT NULL,        -- scoped to the caller's token
    key          TEXT    NOT NULL,
    request_hash BLOB    NOT NULL,        -- SHA-256 of method, path and body
    status       INTEGER,                 -- NULL while the first request is in progress
    content_type TEXT,
    body         BLOB,
    created_at   TEXT    NOT NULL,
    PRIMARY KEY (token_id, key)
);

-- Domain event outbox (internal/events). Rows are written in the same transaction as the state
-- change; the dispatcher fans each one out to subscriber jobs, then sets dispatched_at.
CREATE TABLE events (
    id            INTEGER PRIMARY KEY,
    type          TEXT    NOT NULL,
    payload       TEXT    NOT NULL,   -- JSON
    occurred_at   TEXT    NOT NULL,
    dispatched_at TEXT
);
CREATE INDEX events_undispatched ON events (id) WHERE dispatched_at IS NULL;

-- +goose Down
DROP TABLE events;
DROP TABLE idempotency_keys;
DROP TABLE schedule_cursors;
DROP TABLE jobs;
DROP TABLE tokens;
DROP TABLE recovery_codes;
DROP TABLE invites;
DROP TABLE users;
DROP TABLE setup_token;
DROP TABLE household;
