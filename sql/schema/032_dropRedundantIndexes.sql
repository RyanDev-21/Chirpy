-- +goose Up
-- Covered by the paging indexes from 025, which have the same leading
-- columns plus id: every query that used these can use those instead.
-- Dropping them saves about 17% of message storage.
DROP INDEX IF EXISTS idx_1to1_chat;
DROP INDEX IF EXISTS idx_message_group;

-- +goose Down
CREATE INDEX IF NOT EXISTS idx_message_group ON GroupMessage (group_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_1to1_chat ON message (from_id, to_id, created_at DESC);
