-- +goose Up
-- forwarded messages are labelled "Forwarded" (the original sender isn't shown)
ALTER TABLE message ADD COLUMN forwarded BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE GroupMessage ADD COLUMN forwarded BOOLEAN NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE GroupMessage DROP COLUMN forwarded;
ALTER TABLE message DROP COLUMN forwarded;
