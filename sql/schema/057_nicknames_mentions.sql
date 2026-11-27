-- +goose Up
-- a member's own nickname in one group (NULL: their display name)
ALTER TABLE member_table ADD COLUMN nickname TEXT;

-- who a group message mentions (validated members), and @everyone
ALTER TABLE GroupMessage ADD COLUMN mentions UUID[];
ALTER TABLE GroupMessage ADD COLUMN mention_all BOOLEAN NOT NULL DEFAULT false;

-- "Mentions" notification setting: on by default; mentions notify even in
-- a muted group
ALTER TABLE notification_prefs ADD COLUMN mentions BOOLEAN NOT NULL DEFAULT true;

-- +goose Down
ALTER TABLE notification_prefs DROP COLUMN mentions;
ALTER TABLE GroupMessage DROP COLUMN mention_all;
ALTER TABLE GroupMessage DROP COLUMN mentions;
ALTER TABLE member_table DROP COLUMN nickname;
