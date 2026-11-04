-- +goose Up
-- edited_at: set when the sender edits a message (within 15 minutes).
-- deleted_at (already present) now means "deleted for everyone": the row
-- stays as a placeholder so replies keep their parent, content is wiped.
ALTER TABLE message ADD COLUMN edited_at TIMESTAMP;
ALTER TABLE GroupMessage ADD COLUMN edited_at TIMESTAMP;

-- +goose Down
ALTER TABLE GroupMessage DROP COLUMN edited_at;
ALTER TABLE message DROP COLUMN edited_at;
