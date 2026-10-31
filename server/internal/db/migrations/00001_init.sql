-- +goose Up
CREATE TABLE users (
    id            INTEGER PRIMARY KEY,
    display_name  TEXT    NOT NULL,
    role          TEXT    NOT NULL CHECK (role IN ('parent', 'child')),
    is_owner      INTEGER NOT NULL DEFAULT 0 CHECK (is_owner IN (0, 1) AND (is_owner = 0 OR role = 'parent')),
    username      TEXT    UNIQUE,          -- parents only; kids sign in via paired device + PIN
    password_hash TEXT,                    -- argon2id, parents only
    pin_hash      TEXT,                    -- argon2id, kids (optional)
    oidc_subject  TEXT    UNIQUE,          -- set when linked to an external IdP
    created_at    TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
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

-- +goose Down
DROP TABLE tokens;
DROP TABLE users;
