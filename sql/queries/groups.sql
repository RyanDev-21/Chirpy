-- name: CreateGroup :exec
INSERT INTO chat_groups(id, name, description, max_member, current_member, invite_code)
VALUES ($1, $2, $3, $4, 1, $5);

-- name: CreateGroupLeaderRole :exec
-- joined_at comes from the server clock, like message times, so "messages
-- since I joined" never depends on clock skew between app and database
INSERT INTO member_table(group_id, member_id, role, joined_at)
VALUES ($1, $2, 'Leader', @joined_at::timestamp);

-- name: LockGroup :one
-- serialises membership changes for one group inside a transaction
SELECT max_member, current_member FROM chat_groups WHERE id = $1 FOR UPDATE;

-- name: GetGroupInfoByID :one
SELECT * FROM chat_groups WHERE id = $1;

-- name: GetMemberListByID :many
SELECT member_id FROM member_table WHERE group_id = $1;

-- name: GetGroupLeader :one
SELECT member_id FROM member_table WHERE group_id = $1 AND role = 'Leader' LIMIT 1;

-- name: GetMemberRole :one
SELECT role FROM member_table WHERE group_id = $1 AND member_id = $2;

-- name: AddMember :exec
INSERT INTO member_table(group_id, member_id, joined_at) VALUES ($1, $2, @joined_at::timestamp);

-- name: DeleteMemFromGroup :exec
DELETE FROM member_table WHERE group_id = $1 AND member_id = $2;

-- name: SetGroupMemberCount :exec
UPDATE chat_groups SET current_member = $2, updated_at = NOW() WHERE id = $1;

-- name: ListGroupsForUser :many
SELECT g.id, g.name, g.description, g.max_member, g.current_member, m.role
FROM member_table m
JOIN chat_groups g ON g.id = m.group_id
WHERE m.member_id = $1
ORDER BY lower(g.name);

-- name: ListGroupMembers :many
SELECT m.member_id, u.name, u.display_name, u.avatar_at, m.role
FROM member_table m
JOIN users u ON u.id = m.member_id
WHERE m.group_id = $1
ORDER BY (m.role = 'Leader') DESC, m.joined_at;

-- name: GetGroupByInviteCode :one
SELECT id, name, description, max_member, current_member FROM chat_groups WHERE invite_code = $1;

-- name: GetInviteCode :one
SELECT invite_code FROM chat_groups WHERE id = $1;

-- name: SetInviteCode :exec
UPDATE chat_groups SET invite_code = $2, updated_at = NOW() WHERE id = $1;

-- name: UpdateGroupInfo :one
-- NULL leaves a field unchanged
UPDATE chat_groups
SET name = COALESCE(sqlc.narg(name), name),
    description = COALESCE(sqlc.narg(description), description),
    updated_at = NOW()
WHERE id = @id
RETURNING name, description;

-- name: SetMemberRole :exec
UPDATE member_table SET role = @role WHERE group_id = @group_id AND member_id = @member_id;
