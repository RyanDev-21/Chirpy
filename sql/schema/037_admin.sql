-- +goose Up
-- Admins are granted only with the cmd/admin CLI. A ban is active while
-- banned_at is set; banned_until (NULL = permanent) is lifted by the hourly
-- cleanup. last_seen_at is refreshed when tokens are issued (login/refresh).
ALTER TABLE users ADD COLUMN is_admin BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE users ADD COLUMN banned_at TIMESTAMP;
ALTER TABLE users ADD COLUMN banned_until TIMESTAMP;
ALTER TABLE users ADD COLUMN ban_reason TEXT;
ALTER TABLE users ADD COLUMN last_seen_at TIMESTAMP;
CREATE INDEX idx_users_banned ON users (banned_until) WHERE banned_at IS NOT NULL;

-- every admin action, including searches and views
CREATE TABLE admin_actions (
    id           UUID PRIMARY KEY,
    admin_id     UUID REFERENCES users(id) ON DELETE SET NULL, -- NULL: the CLI or the system
    action       TEXT NOT NULL,
    target_user  UUID,
    target_group UUID,
    detail       TEXT NOT NULL DEFAULT '',
    at           TIMESTAMP NOT NULL
);
CREATE INDEX idx_admin_actions_at ON admin_actions (at DESC);

-- one more email-code purpose: the admin page's second step
ALTER TABLE email_codes DROP CONSTRAINT email_codes_purpose_check;
ALTER TABLE email_codes ADD CONSTRAINT email_codes_purpose_check
    CHECK (purpose IN ('verify_email', 'reset_password', 'admin_login'));

-- +goose Down
DELETE FROM email_codes WHERE purpose = 'admin_login';
ALTER TABLE email_codes DROP CONSTRAINT email_codes_purpose_check;
ALTER TABLE email_codes ADD CONSTRAINT email_codes_purpose_check CHECK (purpose IN ('verify_email', 'reset_password'));
DROP TABLE admin_actions;
DROP INDEX idx_users_banned;
ALTER TABLE users DROP COLUMN last_seen_at;
ALTER TABLE users DROP COLUMN ban_reason;
ALTER TABLE users DROP COLUMN banned_until;
ALTER TABLE users DROP COLUMN banned_at;
ALTER TABLE users DROP COLUMN is_admin;
