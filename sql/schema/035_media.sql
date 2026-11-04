-- +goose Up
-- Photos and videos live in Cloudflare R2; this tracks them. A row starts
-- 'pending' (upload link issued), becomes 'ready' when attached to a
-- message, and gets deleted_at when the object is removed from R2: 24 h
-- after every recipient downloaded it, after 7 days at most, or earlier
-- by the LRU cleanup when storage nears the free 10 GB.
CREATE TABLE media (
    id             UUID PRIMARY KEY,
    owner_id       UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind           TEXT NOT NULL CHECK (kind IN ('image', 'video')),
    content_type   TEXT NOT NULL,
    size           BIGINT NOT NULL CHECK (size > 0),
    status         TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'ready')),
    chat_type      TEXT CHECK (chat_type IN ('private', 'public')),
    chat_id        UUID,           -- the other user or the group, once attached
    message_id     UUID,
    created_at     TIMESTAMP NOT NULL,
    expires_at     TIMESTAMP NOT NULL,
    delivered_at   TIMESTAMP,      -- every recipient has downloaded it
    last_access_at TIMESTAMP NOT NULL,
    deleted_at     TIMESTAMP
);
CREATE INDEX idx_media_message ON media (message_id) WHERE message_id IS NOT NULL;
CREATE INDEX idx_media_owner_day ON media (owner_id, created_at);
CREATE INDEX idx_media_live_lru ON media (last_access_at) WHERE deleted_at IS NULL;

CREATE TABLE media_downloads (
    media_id UUID NOT NULL REFERENCES media(id) ON DELETE CASCADE,
    user_id  UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    PRIMARY KEY (media_id, user_id)
);

-- +goose Down
DROP TABLE media_downloads;
DROP TABLE media;
