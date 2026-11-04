-- +goose Up
-- group pictures, stored like user avatars (256x256 JPEG, ~20 KB)
CREATE TABLE group_avatars (
    group_id   UUID PRIMARY KEY REFERENCES chat_groups(id) ON DELETE CASCADE,
    image      BYTEA NOT NULL CHECK (octet_length(image) <= 262144),
    updated_at TIMESTAMP NOT NULL
);
ALTER TABLE chat_groups ADD COLUMN avatar_at TIMESTAMP;

-- changing the email address: the code goes to the new address, which is
-- kept with the code until it is confirmed
ALTER TABLE email_codes ADD COLUMN new_email TEXT;
ALTER TABLE email_codes DROP CONSTRAINT email_codes_purpose_check;
ALTER TABLE email_codes ADD CONSTRAINT email_codes_purpose_check
    CHECK (purpose IN ('verify_email', 'reset_password', 'admin_login', 'change_email'));

-- +goose Down
DELETE FROM email_codes WHERE purpose = 'change_email';
ALTER TABLE email_codes DROP CONSTRAINT email_codes_purpose_check;
ALTER TABLE email_codes ADD CONSTRAINT email_codes_purpose_check
    CHECK (purpose IN ('verify_email', 'reset_password', 'admin_login'));
ALTER TABLE email_codes DROP COLUMN new_email;
ALTER TABLE chat_groups DROP COLUMN avatar_at;
DROP TABLE group_avatars;
