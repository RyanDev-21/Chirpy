-- +goose Up
ALTER TABLE users ADD COLUMN password TEXT NOT NULL;

-- +goose Down
ALTER TABLE users DROP COLUMN password;
