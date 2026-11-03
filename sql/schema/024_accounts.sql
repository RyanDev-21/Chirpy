-- +goose Up
-- Accounts: email verification, password reset, Google sign-in.

-- NULL password = no password sign-in (e.g. a Google-only account)
ALTER TABLE users ALTER COLUMN password DROP NOT NULL;
ALTER TABLE users ADD COLUMN email_verified_at TIMESTAMP;
-- accounts created before verification existed are trusted as-is
UPDATE users SET email_verified_at = created_at;

-- one row per external sign-in method linked to a user
CREATE TABLE user_identities (
    provider   TEXT NOT NULL,              -- 'google' (later 'apple')
    subject    TEXT NOT NULL,              -- the provider's permanent account id
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    email      TEXT NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    PRIMARY KEY (provider, subject)
);
CREATE INDEX idx_user_identities_user ON user_identities(user_id);

-- short-lived email codes; at most one live code per user and purpose.
-- code_hash is an HMAC of the 6-digit code keyed with the server secret,
-- so a database copy does not reveal codes.
CREATE TABLE email_codes (
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    purpose    TEXT NOT NULL CHECK (purpose IN ('verify_email', 'reset_password')),
    code_hash  TEXT NOT NULL,
    attempts   INT NOT NULL DEFAULT 0,
    expires_at TIMESTAMP NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, purpose)
);

-- +goose Down
DROP TABLE email_codes;
DROP TABLE user_identities;
ALTER TABLE users DROP COLUMN email_verified_at;
UPDATE users SET password = '' WHERE password IS NULL;
ALTER TABLE users ALTER COLUMN password SET NOT NULL;
