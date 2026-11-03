-- name: CreateGroup :exec
INSERT INTO chat_groups(id, name, description, max_member, current_member)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (id) DO NOTHING;

-- name: CreateGroupLeaderRole :exec
INSERT INTO member_table(group_id, member_id, role)
VALUES ($1, $2, $3)
ON CONFLICT (group_id, member_id) DO NOTHING;

-- name: SearchInfoByName :one
SELECT * FROM chat_groups WHERE name = $1;

-- name: GetGroupInfoByID :one
SELECT * FROM chat_groups WHERE id = $1;

-- name: GetMemberListByID :many
SELECT member_id FROM member_table WHERE group_id = $1;

-- name: UpdateGroupCurrentMemberByID :exec
UPDATE chat_groups
SET current_member = (SELECT COUNT(*) FROM member_table WHERE group_id = $1), updated_at = NOW()
WHERE id = $1;

-- name: AddMember :exec
INSERT INTO member_table(group_id, member_id) VALUES ($1, $2)
ON CONFLICT (group_id, member_id) DO NOTHING;

-- name: DeleteMemFromGroup :execrows
DELETE FROM member_table WHERE group_id = $1 AND member_id = $2;

-- name: GetMemFromGroup :one
SELECT * FROM member_table WHERE group_id = $1 AND member_id = $2;

-- name: GetGroupLeader :one
SELECT member_id FROM member_table WHERE group_id = $1 AND role = 'Leader' LIMIT 1;
