-- +goose Up
-- Avatars are small (256x256 JPEG, ~20 KB), so they live in postgres for
-- now; avatar_at on users versions the URL clients cache.
CREATE TABLE user_avatars (
    user_id    UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    image      BYTEA NOT NULL CHECK (octet_length(image) <= 262144),
    updated_at TIMESTAMP NOT NULL
);
ALTER TABLE users ADD COLUMN avatar_at TIMESTAMP;

-- +goose Down
ALTER TABLE users DROP COLUMN avatar_at;
DROP TABLE user_avatars;
