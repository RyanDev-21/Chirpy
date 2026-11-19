-- +goose Up
-- notes: a short text shown to friends for 24 hours (one per user)
CREATE TABLE notes (
    user_id    UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    text       TEXT NOT NULL,
    created_at TIMESTAMP NOT NULL,
    expires_at TIMESTAMP NOT NULL
);
CREATE INDEX idx_notes_expires ON notes (expires_at);

-- streaks: friends who both sent each other a private message every day
-- in a row (days in Thailand time). One row per pair, user_lo < user_hi.
CREATE TABLE streaks (
    user_lo  UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    user_hi  UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    lo_day   DATE,          -- last day user_lo sent one
    hi_day   DATE,          -- last day user_hi sent one
    done_day DATE,          -- last day both did
    count    INT NOT NULL DEFAULT 0,
    PRIMARY KEY (user_lo, user_hi),
    CHECK (user_lo < user_hi)
);

ALTER TABLE users ADD COLUMN hide_streaks BOOLEAN NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE users DROP COLUMN hide_streaks;
DROP TABLE streaks;
DROP TABLE notes;
