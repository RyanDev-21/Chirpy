-- +goose Up
-- One row per phone. The token is the key: when a phone signs in as another
-- user, its row moves to that user, so it never gets the previous user's
-- notifications.
CREATE TABLE user_devices (
    token        TEXT PRIMARY KEY CHECK (length(token) BETWEEN 1 AND 4096),
    user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    platform     TEXT NOT NULL DEFAULT 'android',
    created_at   TIMESTAMP NOT NULL DEFAULT NOW(),
    last_seen_at TIMESTAMP NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_user_devices_user ON user_devices (user_id);

-- which kinds of notification a user wants; no row = all on
CREATE TABLE notification_prefs (
    user_id   UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    private   BOOLEAN NOT NULL DEFAULT true,
    groups    BOOLEAN NOT NULL DEFAULT true,
    friends   BOOLEAN NOT NULL DEFAULT true,
    reactions BOOLEAN NOT NULL DEFAULT true
);

-- muted chats: chat_type 'private' (chat_id = the other user) or 'public'
-- (chat_id = the group); until NULL = muted until unmuted
CREATE TABLE chat_mutes (
    user_id   UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    chat_type TEXT NOT NULL CHECK (chat_type IN ('private', 'public')),
    chat_id   UUID NOT NULL,
    until     TIMESTAMP,
    PRIMARY KEY (user_id, chat_type, chat_id)
);

-- +goose Down
DROP TABLE chat_mutes;
DROP TABLE notification_prefs;
DROP TABLE user_devices;
