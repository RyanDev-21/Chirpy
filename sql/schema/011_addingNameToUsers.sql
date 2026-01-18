-- +goose Up
ALTER TABLE users ADD COLUMN name VARCHAR(25) UNIQUE NOT NULL;

-- +goose Down
ALTER TABLE users DROP COLUMN name;
