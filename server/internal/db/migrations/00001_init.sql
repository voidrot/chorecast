-- +goose Up
-- The household this server hosts. There is exactly one (id = 1); its existence means first-run
-- setup is complete (internal/setup).
CREATE TABLE household (
    id         INTEGER PRIMARY KEY CHECK (id = 1),
    name       TEXT    NOT NULL,
    timezone   TEXT    NOT NULL,                  -- IANA name
    day_end    TEXT    NOT NULL DEFAULT '00:00',  -- HH:MM; 00:00 = midnight at the end of the day
    week_start INTEGER NOT NULL DEFAULT 1 CHECK (week_start BETWEEN 0 AND 6), -- 0 = Sunday
    allow_negative_balance INTEGER NOT NULL DEFAULT 0 CHECK (allow_negative_balance IN (0, 1)),
    sibling_visibility     TEXT    NOT NULL DEFAULT 'private' CHECK (sibling_visibility IN ('private', 'leaderboard')),
    media_retention_days   INTEGER NOT NULL DEFAULT 30 CHECK (media_retention_days BETWEEN 1 AND 3650),
    auto_approve_hours     INTEGER CHECK (auto_approve_hours BETWEEN 1 AND 720), -- NULL = never
    offline_grace_minutes  INTEGER NOT NULL DEFAULT 120 CHECK (offline_grace_minutes BETWEEN 0 AND 1440),
    default_chore_minutes  INTEGER NOT NULL DEFAULT 10 CHECK (default_chore_minutes BETWEEN 1 AND 240), -- fair auto-pick load for chores without an estimate
    -- JSON [{"days": N, "points": P}] sorted by days, distinct days; [] = no streak bonuses (internal/streaks)
    streak_milestones      TEXT    NOT NULL DEFAULT '[{"days":3,"points":5},{"days":7,"points":15},{"days":30,"points":50}]'
                                   CHECK (json_valid(streak_milestones)),
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

-- Where a chore happens and what it needs (simple catalogs; photos come with the media pipeline).
CREATE TABLE locations (
    id          INTEGER PRIMARY KEY,
    name        TEXT    NOT NULL UNIQUE,
    description TEXT    NOT NULL DEFAULT '',
    created_at  TEXT    NOT NULL
);

CREATE TABLE equipment (
    id          INTEGER PRIMARY KEY,
    name        TEXT    NOT NULL UNIQUE,
    description TEXT    NOT NULL DEFAULT '',
    created_at  TEXT    NOT NULL
);

-- Chore definitions (internal/chores): what, when (schedule), who (assignment), how it's checked.
CREATE TABLE chores (
    id                INTEGER PRIMARY KEY,
    name              TEXT    NOT NULL,
    description       TEXT    NOT NULL DEFAULT '',
    points            INTEGER NOT NULL DEFAULT 0 CHECK (points BETWEEN 0 AND 100000),
    penalty           INTEGER NOT NULL DEFAULT 0 CHECK (penalty BETWEEN 0 AND 100000), -- deducted when missed
    estimated_minutes INTEGER CHECK (estimated_minutes BETWEEN 1 AND 1440),
    min_age           INTEGER CHECK (min_age BETWEEN 1 AND 25),
    steps             TEXT    NOT NULL DEFAULT '[]', -- JSON array of checklist step texts
    slots             TEXT    NOT NULL DEFAULT '[]', -- JSON array of HH:MM due times; empty = due at day close
    schedule          TEXT,                          -- JSON schedule rule (internal/schedule); NULL = not scheduled
    -- Assignment (internal/assign). assign_kids: fixed kids, rotation order, auto pool (empty = all) or shared group.
    assign_mode       TEXT    NOT NULL DEFAULT 'everyone' CHECK (assign_mode IN ('fixed', 'everyone', 'rotation', 'auto', 'shared')),
    assign_kids       TEXT    NOT NULL DEFAULT '[]',
    shared_points     TEXT    NOT NULL DEFAULT 'completer_takes_all' CHECK (shared_points IN ('completer_takes_all', 'split_evenly')),
    shared_penalty    TEXT    NOT NULL DEFAULT 'all' CHECK (shared_penalty IN ('all', 'none')),
    rotation_state    TEXT    NOT NULL DEFAULT '{}', -- JSON assign.State (cursor, owed) after the last instance before the horizon (internal/horizon); reset to {} when the rotation changes
    requires_approval INTEGER NOT NULL DEFAULT 1 CHECK (requires_approval IN (0, 1)),
    escalate          INTEGER NOT NULL DEFAULT 0 CHECK (escalate IN (0, 1)),
    -- NULL = household default; 0 = never; N = approve after N hours
    auto_approve_hours INTEGER CHECK (auto_approve_hours BETWEEN 0 AND 720),
    evidence_photos       INTEGER NOT NULL DEFAULT 0 CHECK (evidence_photos BETWEEN 0 AND 5),
    evidence_before_after INTEGER NOT NULL DEFAULT 0 CHECK (evidence_before_after IN (0, 1)),
    evidence_video        INTEGER NOT NULL DEFAULT 0 CHECK (evidence_video IN (0, 1)),
    evidence_checklist    INTEGER NOT NULL DEFAULT 0 CHECK (evidence_checklist IN (0, 1)),
    evidence_note         INTEGER NOT NULL DEFAULT 0 CHECK (evidence_note IN (0, 1)),
    location_id       INTEGER REFERENCES locations (id) ON DELETE SET NULL,
    disabled          INTEGER NOT NULL DEFAULT 0 CHECK (disabled IN (0, 1)),
    created_at        TEXT    NOT NULL,
    updated_at        TEXT    NOT NULL
);

CREATE TABLE chore_equipment (
    chore_id     INTEGER NOT NULL REFERENCES chores (id) ON DELETE CASCADE,
    equipment_id INTEGER NOT NULL REFERENCES equipment (id) ON DELETE CASCADE,
    PRIMARY KEY (chore_id, equipment_id)
);

-- Per-kid exceptions to a chore's minimum age: allow a younger kid, or exclude a kid.
CREATE TABLE chore_eligibility (
    chore_id INTEGER NOT NULL REFERENCES chores (id) ON DELETE CASCADE,
    kid_id   INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    allow    INTEGER NOT NULL CHECK (allow IN (0, 1)),
    PRIMARY KEY (chore_id, kid_id)
);

-- Paired Android devices (internal/devices): kid-bound (user_id = the kid) or shared (user_id NULL).
-- The device authenticates with its `device` token; revoking it deletes this row.
CREATE TABLE devices (
    id                  INTEGER PRIMARY KEY,
    user_id             INTEGER REFERENCES users (id) ON DELETE CASCADE,
    name                TEXT    NOT NULL,
    app_version         TEXT    NOT NULL DEFAULT '',
    created_at          TEXT    NOT NULL,
    elevate_failures    INTEGER NOT NULL DEFAULT 0, -- wrong parent PINs in a row on this device
    elevate_locked_until TEXT                       -- 5 failures lock elevation for 15 minutes
);

-- Single-use pairing codes (5 minutes). Only the SHA-256 of the normalized code is stored.
CREATE TABLE pairing_codes (
    id         INTEGER PRIMARY KEY,
    code_hash  BLOB    NOT NULL UNIQUE,
    user_id    INTEGER REFERENCES users (id) ON DELETE CASCADE, -- the kid; NULL = shared family device
    created_by INTEGER REFERENCES users (id) ON DELETE SET NULL,
    created_at TEXT    NOT NULL,
    expires_at TEXT    NOT NULL,
    used_at    TEXT
);

-- Opaque bearer/session tokens. Only the SHA-256 of the token is stored.
CREATE TABLE tokens (
    id           INTEGER PRIMARY KEY,
    token_hash   BLOB    NOT NULL UNIQUE,
    kind         TEXT    NOT NULL CHECK (kind IN ('web_session', 'app', 'device', 'device_session', 'display', 'api')),
    user_id      INTEGER REFERENCES users (id) ON DELETE CASCADE, -- NULL for shared-device tokens
    -- device / device_session: the paired device this token or session belongs to. Deleting the
    -- device revokes its token and ends its kid sessions and parent elevations.
    device_id    INTEGER REFERENCES devices (id) ON DELETE CASCADE,
    label        TEXT    NOT NULL DEFAULT '',
    created_at   TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    last_used_at TEXT,
    expires_at   TEXT,
    -- Sessions and app tokens always belong to a person; devices, displays and API keys may not.
    CHECK (user_id IS NOT NULL OR kind IN ('device', 'display', 'api')),
    CHECK (kind != 'device_session' OR device_id IS NOT NULL),
    CHECK (device_id IS NULL OR kind IN ('device', 'device_session'))
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

-- ==== points ledger (#26) ====
-- Append-only points ledger (internal/ledger). A kid's balance is SUM(amount); nothing else stores
-- it. Mistakes are fixed with a reversal entry (reverses_id), never by editing or deleting a row.
-- source_kind/source_id name what the entry is for (e.g. 'chore_instance', <id>). There is no
-- foreign key: sources live in tables of other features (some not built yet), and the ledger
-- must outlive them.
CREATE TABLE points_entries (
    id               INTEGER PRIMARY KEY,
    kid_id           INTEGER NOT NULL REFERENCES users (id),
    kind             TEXT    NOT NULL CHECK (kind IN ('chore', 'chore_penalty', 'streak_bonus', 'adjustment', 'reversal')),
    amount           INTEGER NOT NULL,
    requested_amount INTEGER,                  -- the asked-for amount when allow_negative_balance = 0 clamped it
    local_date       TEXT    NOT NULL,         -- YYYY-MM-DD: the kid's household-local day the entry belongs to
    source_kind      TEXT,
    source_id        INTEGER,
    reverses_id      INTEGER UNIQUE REFERENCES points_entries (id), -- an entry is reversed at most once
    note             TEXT    NOT NULL DEFAULT '',
    created_by       INTEGER REFERENCES users (id), -- the parent (or kid); NULL = the system
    created_at       TEXT    NOT NULL,
    CHECK ((kind = 'reversal') = (reverses_id IS NOT NULL)),
    CHECK (kind NOT IN ('adjustment', 'reversal') OR note != ''),
    CHECK ((source_kind IS NULL) = (source_id IS NULL))
);
CREATE INDEX points_entries_kid ON points_entries (kid_id, id, amount);
-- At most one chore, penalty or streak bonus entry per kid and source, so at-least-once callers
-- (day close, approvals) can't post twice. Reversals copy their original's source and are
-- excluded; undoing a reversal reverses it again.
CREATE UNIQUE INDEX points_entries_once ON points_entries (kind, source_kind, source_id, kid_id)
    WHERE source_id IS NOT NULL AND kind IN ('chore', 'chore_penalty', 'streak_bonus');

-- ==== Chore instances (#23) ====
-- One due instance of a chore for one kid (or a shared group), generated by the rolling horizon
-- (internal/horizon) for [today, today + 7] and moved through its lifecycle by later features.
-- Identity: (chore, occurrence_date, slot, seq, kid). kid_id NULL = a shared chore's instance;
-- the members who may complete it are in chore_instance_members. Ids are never reused
-- (AUTOINCREMENT): clients, evidence and ledger entries refer to instances by id. Once a chore
-- has history (an instance that's touched or already due), parents disable it instead of
-- deleting it; deleting a chore without history removes its open instances first.
CREATE TABLE chore_instances (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    chore_id            INTEGER NOT NULL REFERENCES chores (id),
    kid_id              INTEGER REFERENCES users (id) ON DELETE CASCADE, -- the assignee; NULL = shared
    occurrence_date     TEXT    NOT NULL,             -- YYYY-MM-DD household date (flexible: the week's first active day)
    slot                TEXT    NOT NULL DEFAULT '',  -- HH:MM due time; '' = due at day close (or week close when flexible)
    seq                 INTEGER NOT NULL DEFAULT 0,   -- flexible: which of the week's completions (0..N-1); else 0
    due_at              TEXT    NOT NULL,
    flexible            INTEGER NOT NULL DEFAULT 0 CHECK (flexible IN (0, 1)),
    status              TEXT    NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'submitted', 'approved', 'missed', 'excused')),
    auto_assigned       INTEGER NOT NULL DEFAULT 0 CHECK (auto_assigned IN (0, 1)),       -- picked by the fair auto-pick
    manually_reassigned INTEGER NOT NULL DEFAULT 0 CHECK (manually_reassigned IN (0, 1)), -- a parent or swap changed the assignee
    reassigned_from     INTEGER REFERENCES users (id) ON DELETE SET NULL, -- the planned kid a reassignment replaced (the first one); the horizon counts the instance as theirs
    rotation_state      TEXT,                         -- rotation: JSON assign.State after this instance's turn
    -- Lifecycle (internal/instances, #25). The last completion is kept after a rejection or an
    -- approval reversal, so a completed instance stays touched (never regenerated).
    completed_by        INTEGER REFERENCES users (id) ON DELETE SET NULL, -- the kid who completed it (shared: the completer)
    completed_at        TEXT,
    rejection_count     INTEGER NOT NULL DEFAULT 0,   -- rows in chore_instance_rejections
    created_at          TEXT    NOT NULL,
    updated_at          TEXT    NOT NULL
);
CREATE UNIQUE INDEX chore_instances_identity ON chore_instances (chore_id, occurrence_date, slot, seq, COALESCE(kid_id, 0));
CREATE INDEX chore_instances_by_date ON chore_instances (occurrence_date);
CREATE INDEX chore_instances_by_kid ON chore_instances (kid_id, occurrence_date);

-- The group of a shared instance: the members eligible on its date, who may complete it.
CREATE TABLE chore_instance_members (
    instance_id INTEGER NOT NULL REFERENCES chore_instances (id) ON DELETE CASCADE,
    kid_id      INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    PRIMARY KEY (instance_id, kid_id)
);
CREATE INDEX chore_instance_members_by_kid ON chore_instance_members (kid_id);

-- ==== Media pipeline (#75) ====
-- Server-generated secrets, one row per purpose, created on first use (32 random bytes).
-- "media_url": internal/media derives the media URL signing key from it; rotate it with
-- `chorecast admin rotate-media-key`. Database backups contain these secrets.
CREATE TABLE server_keys (
    name       TEXT PRIMARY KEY,
    key        BLOB NOT NULL,
    created_at TEXT NOT NULL
);

-- Uploaded photos (internal/media). The files live in storage under storage_key / thumb_key; the
-- row stays after retention deletes them (status = 'expired'). owner_kind/owner_id say what the
-- media belongs to: 'pending' (owner_id NULL) until it is attached to evidence, a rejection, a
-- chore or a piece of equipment.
CREATE TABLE media (
    id           INTEGER PRIMARY KEY,
    owner_kind   TEXT    NOT NULL DEFAULT 'pending' CHECK (owner_kind IN ('pending', 'evidence', 'rejection', 'chore', 'equipment')),
    owner_id     INTEGER,
    uploaded_by  INTEGER REFERENCES users (id) ON DELETE SET NULL,
    storage_key  TEXT    NOT NULL UNIQUE,
    thumb_key    TEXT    UNIQUE,
    kind         TEXT    NOT NULL CHECK (kind IN ('photo')),
    mime         TEXT    NOT NULL,
    bytes        INTEGER NOT NULL,
    width        INTEGER NOT NULL,
    height       INTEGER NOT NULL,
    thumb_width  INTEGER,
    thumb_height INTEGER,
    sha256       BLOB    NOT NULL,             -- of the decoded pixels before orientation and resizing (media.Photo.SHA256)
    dhash        INTEGER NOT NULL,             -- 64-bit difference hash (uint64 stored as int64)
    status       TEXT    NOT NULL DEFAULT 'ready' CHECK (status IN ('ready', 'expired')),
    keep         INTEGER NOT NULL DEFAULT 0 CHECK (keep IN (0, 1)),
    created_at   TEXT    NOT NULL,
    expires_at   TEXT,                         -- NULL = never (set when the owning instance resolves)
    CHECK ((owner_kind = 'pending') = (owner_id IS NULL))
);

-- Hashes of every kid evidence photo, kept after the media is deleted so reuse is still caught.
-- An exact SHA-256 match is rejected on upload; dhash is for near-duplicate flagging.
CREATE TABLE media_hashes (
    sha256     BLOB    PRIMARY KEY,
    dhash      INTEGER NOT NULL,
    kid_id     INTEGER REFERENCES users (id) ON DELETE SET NULL,
    media_id   INTEGER REFERENCES media (id) ON DELETE SET NULL,
    created_at TEXT    NOT NULL
);

-- ==== Day and week close (#24) ====
-- Closes look up the open instances already due (internal/dayclose).
CREATE INDEX chore_instances_open_due ON chore_instances (due_at) WHERE status = 'open';

-- ==== Streaks (#30) ====
-- Each kid's evaluated days (internal/streaks): counted (every required chore approved, submitted
-- or excused), broken, or none (no required chores: neither breaks nor extends). A counted day is
-- final; the others are re-checked at the kid's next closes, so a late completion or an excuse
-- repairs them.
CREATE TABLE streak_days (
    kid_id  INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    day     TEXT    NOT NULL, -- YYYY-MM-DD
    outcome TEXT    NOT NULL CHECK (outcome IN ('counted', 'broken', 'none')),
    PRIMARY KEY (kid_id, day)
);

-- The daily streak derived from streak_days, kept so reads are cheap.
CREATE TABLE kid_streaks (
    kid_id   INTEGER PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    current  INTEGER NOT NULL,
    best     INTEGER NOT NULL,
    last_day TEXT    NOT NULL -- YYYY-MM-DD: the latest day evaluated
);

-- Per-chore streaks (display only), recomputed from instance history at each evaluation.
CREATE TABLE kid_chore_streaks (
    kid_id   INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    chore_id INTEGER NOT NULL REFERENCES chores (id) ON DELETE CASCADE,
    current  INTEGER NOT NULL,
    best     INTEGER NOT NULL,
    PRIMARY KEY (kid_id, chore_id)
);
-- ==== Instance state machine (#25) ====
-- Every rejection of a submitted instance (internal/instances): a quick-pick reason, text, or both.
CREATE TABLE chore_instance_rejections (
    id          INTEGER PRIMARY KEY,
    instance_id INTEGER NOT NULL REFERENCES chore_instances (id) ON DELETE CASCADE,
    kid_id      INTEGER REFERENCES users (id) ON DELETE SET NULL, -- the completer whose submission was rejected
    reason_code TEXT    NOT NULL DEFAULT '' CHECK (reason_code IN ('', 'not_done', 'not_finished', 'needs_redo', 'evidence_unclear')),
    reason_text TEXT    NOT NULL DEFAULT '',
    rejected_by INTEGER REFERENCES users (id) ON DELETE SET NULL,
    created_at  TEXT    NOT NULL,
    CHECK (reason_code != '' OR reason_text != '')
);
CREATE INDEX chore_instance_rejections_by_instance ON chore_instance_rejections (instance_id, id);

-- +goose Down
DROP TABLE kid_chore_streaks;
DROP TABLE kid_streaks;
DROP TABLE streak_days;
DROP TABLE chore_instance_rejections;
DROP TABLE chore_instance_members;
DROP TABLE chore_instances;
DROP TABLE media_hashes;
DROP TABLE media;
DROP TABLE server_keys;
DROP TABLE points_entries;
DROP TABLE events;
DROP TABLE idempotency_keys;
DROP TABLE schedule_cursors;
DROP TABLE jobs;
DROP TABLE tokens;
DROP TABLE pairing_codes;
DROP TABLE devices;
DROP TABLE chore_eligibility;
DROP TABLE chore_equipment;
DROP TABLE chores;
DROP TABLE equipment;
DROP TABLE locations;
DROP TABLE recovery_codes;
DROP TABLE invites;
DROP TABLE users;
DROP TABLE setup_token;
DROP TABLE household;
