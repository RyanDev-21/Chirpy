-- +goose Up
-- Message search. Trigrams, not word search: Thai, Burmese, Chinese and
-- Japanese have no spaces between words, which word-based full-text search
-- needs; trigrams find any piece of text in any script.
CREATE EXTENSION IF NOT EXISTS pg_trgm;

-- only private chats and groups of up to 50 members are indexed (Neon's
-- 0.5 GB); a group message is searchable if the group was that small when
-- it was sent
ALTER TABLE GroupMessage ADD COLUMN searchable BOOLEAN NOT NULL DEFAULT false;
UPDATE GroupMessage g SET searchable = true
FROM chat_groups c WHERE c.id = g.group_id AND c.current_member <= 50;

CREATE INDEX idx_message_search ON message USING gin (lower(content) gin_trgm_ops)
    WHERE deleted_at IS NULL;
CREATE INDEX idx_groupmessage_search ON GroupMessage USING gin (lower(content) gin_trgm_ops)
    WHERE searchable AND deleted_at IS NULL;

-- +goose Down
DROP INDEX idx_groupmessage_search;
DROP INDEX idx_message_search;
ALTER TABLE GroupMessage DROP COLUMN searchable;
