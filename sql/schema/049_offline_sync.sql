-- +goose Up
-- sent_ids: which client_msg_id each sender already used, so a retry days
-- later (phone offline, the first answer lost) returns the same message
-- instead of sending it twice. Rows are deleted after 7 days.
CREATE TABLE sent_ids (
    from_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    client_id  UUID NOT NULL,
    msg_id     UUID NOT NULL,
    created_at TIMESTAMP NOT NULL,
    PRIMARY KEY (from_id, client_id)
);

-- changed_at: the last edit, delete or reaction change, so a phone that was
-- offline can fetch what changed in messages it already has
ALTER TABLE message ADD COLUMN changed_at TIMESTAMP;
ALTER TABLE GroupMessage ADD COLUMN changed_at TIMESTAMP;
CREATE INDEX idx_message_from_changed ON message (from_id, changed_at) WHERE changed_at IS NOT NULL;
CREATE INDEX idx_message_to_changed ON message (to_id, changed_at) WHERE changed_at IS NOT NULL;
CREATE INDEX idx_groupmessage_changed ON GroupMessage (group_id, changed_at) WHERE changed_at IS NOT NULL;

-- +goose Down
DROP INDEX idx_groupmessage_changed;
DROP INDEX idx_message_to_changed;
DROP INDEX idx_message_from_changed;
ALTER TABLE GroupMessage DROP COLUMN changed_at;
ALTER TABLE message DROP COLUMN changed_at;
DROP TABLE sent_ids;
