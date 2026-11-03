-- +goose Up
-- Read markers for group chats (private chats use seen_messages): the last
-- group message a member has read, for unread counts.
CREATE TABLE group_reads (
    group_id   UUID NOT NULL REFERENCES chat_groups(id) ON DELETE CASCADE,
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    message_id UUID NOT NULL REFERENCES GroupMessage(id) ON DELETE CASCADE,
    updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
    PRIMARY KEY (group_id, user_id)
);
-- paging older history and unread counts walk messages by time
CREATE INDEX IF NOT EXISTS idx_message_pair_time ON message (from_id, to_id, created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_groupmessage_time ON GroupMessage (group_id, created_at DESC, id DESC);

-- +goose Down
DROP INDEX IF EXISTS idx_groupmessage_time;
DROP INDEX IF EXISTS idx_message_pair_time;
DROP TABLE group_reads;
