-- +goose Up
-- Refresh tokens are now stored as SHA-256 hashes. Rows written before this
-- hold the raw token, which can never match a hash lookup, so remove them
-- (those sessions sign in again).
DELETE FROM refresh_tokens;
COMMENT ON COLUMN refresh_tokens.token IS 'hex SHA-256 of the refresh token; the token itself is never stored';

-- +goose Down
COMMENT ON COLUMN refresh_tokens.token IS NULL;
