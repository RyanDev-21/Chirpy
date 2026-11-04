-- +goose Up
-- hide_last_seen: friends see neither "online" nor "last seen", and (as in
-- most chat apps) the user can't see theirs either.
ALTER TABLE users ADD COLUMN hide_last_seen BOOLEAN NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE users DROP COLUMN hide_last_seen;
