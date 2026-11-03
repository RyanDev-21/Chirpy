-- +goose Up
-- keep only the newest row per (chat_id, seen_id) before adding the constraint
DELETE FROM seen_messages a
USING seen_messages b
WHERE a.chat_id = b.chat_id
  AND a.seen_id = b.seen_id
  AND a.updated_time < b.updated_time;
ALTER TABLE seen_messages
    ADD CONSTRAINT seen_messages_chat_seen_key UNIQUE (chat_id, seen_id);
CREATE INDEX IF NOT EXISTS idx_refresh_tokens_user ON refresh_tokens(user_id);

-- +goose Down
DROP INDEX IF EXISTS idx_refresh_tokens_user;
ALTER TABLE seen_messages DROP CONSTRAINT seen_messages_chat_seen_key;
