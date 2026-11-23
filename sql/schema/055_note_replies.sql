-- +goose Up
-- a private message answering the recipient's note keeps the note's text
-- (copied by the server when sent), so the reply still makes sense after
-- the note expires
ALTER TABLE message ADD COLUMN note_text TEXT;

-- replies to stories, for "who saw it" (only story replies are indexed)
CREATE INDEX idx_message_story ON message (story_id, from_id, created_at DESC) WHERE story_id IS NOT NULL;

-- +goose Down
DROP INDEX idx_message_story;
ALTER TABLE message DROP COLUMN note_text;
