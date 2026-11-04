-- +goose Up
ALTER TABLE users ADD COLUMN bio VARCHAR(150) NOT NULL DEFAULT '';
-- when the username was last changed (changes are limited to one per 30 days)
ALTER TABLE users ADD COLUMN name_changed_at TIMESTAMP;

-- +goose Down
ALTER TABLE users DROP COLUMN name_changed_at;
ALTER TABLE users DROP COLUMN bio;
