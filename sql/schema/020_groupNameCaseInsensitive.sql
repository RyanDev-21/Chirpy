-- +goose Up
-- Group names are unique ignoring case (enforced in postgres instead of a
-- Redis reservation). Existing case-insensitive duplicates get a suffix.
UPDATE chat_groups g
SET name = left(g.name, 25) || '-' || left(g.id::text, 4)
FROM (
    SELECT id, ROW_NUMBER() OVER (PARTITION BY lower(name) ORDER BY created_at, id) AS rn
    FROM chat_groups
) d
WHERE d.id = g.id AND d.rn > 1;
CREATE UNIQUE INDEX chat_groups_name_lower_key ON chat_groups (lower(name));
-- current_member is now trusted inside the join/leave transaction; the old
-- code never kept it up to date, so recompute it once
UPDATE chat_groups g
SET current_member = (SELECT count(*) FROM member_table m WHERE m.group_id = g.id);

-- +goose Down
DROP INDEX chat_groups_name_lower_key;
