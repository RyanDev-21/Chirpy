-- +goose Up
-- chat_id is "<uuid>_<uuid>": 36 + 1 + 36 = 73 characters, one more than
-- VARCHAR(72) allowed, so every read receipt failed to insert.
ALTER TABLE seen_messages ALTER COLUMN chat_id TYPE TEXT;

-- +goose Down
ALTER TABLE seen_messages ALTER COLUMN chat_id TYPE VARCHAR(72);
