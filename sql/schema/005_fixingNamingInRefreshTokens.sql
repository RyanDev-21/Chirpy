-- +goose Up
ALTER TABLE refresh_tokens RENAME  COLUMN udpated_at TO updated_at;

-- +goose Down 
ALTER TABLE refresh_tokens RENAME COLUMN updated_at TO udpated_at;
