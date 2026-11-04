-- +goose Up
-- Roles: admin (the one who runs the group; was "Leader"), moderator (with
-- the permissions the admin picked) and member.
UPDATE member_table SET role = 'admin' WHERE role = 'Leader';
UPDATE member_table SET role = 'member' WHERE role NOT IN ('admin', 'member');
ALTER TABLE member_table ALTER COLUMN role SET DEFAULT 'member';
ALTER TABLE member_table ADD CONSTRAINT member_table_role_check
    CHECK (role IN ('admin', 'moderator', 'member'));
ALTER TABLE member_table ADD COLUMN permissions TEXT[] NOT NULL DEFAULT '{}'
    CHECK (permissions <@ ARRAY['add_members', 'remove_members', 'delete_messages', 'edit_info', 'manage_invite']::text[]);

-- public groups can be found by name and joined without a code
ALTER TABLE chat_groups ADD COLUMN visibility TEXT NOT NULL DEFAULT 'private'
    CHECK (visibility IN ('private', 'public'));
-- names no longer have to be unique: two friend circles can both be "family"
DROP INDEX chat_groups_name_lower_key;
CREATE INDEX idx_chat_groups_public_name ON chat_groups (lower(name) text_pattern_ops) WHERE visibility = 'public';

-- +goose Down
DROP INDEX idx_chat_groups_public_name;
UPDATE chat_groups g
SET name = left(g.name, 25) || '-' || left(g.id::text, 4)
FROM (
    SELECT id, ROW_NUMBER() OVER (PARTITION BY lower(name) ORDER BY created_at, id) AS rn
    FROM chat_groups
) d
WHERE d.id = g.id AND d.rn > 1;
CREATE UNIQUE INDEX chat_groups_name_lower_key ON chat_groups (lower(name));
ALTER TABLE chat_groups DROP COLUMN visibility;
ALTER TABLE member_table DROP COLUMN permissions;
ALTER TABLE member_table DROP CONSTRAINT member_table_role_check;
UPDATE member_table SET role = 'member' WHERE role = 'moderator';
UPDATE member_table SET role = 'Leader' WHERE role = 'admin';
