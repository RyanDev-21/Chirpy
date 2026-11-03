-- name: CreateGroup :exec
INSERT INTO chat_groups(id, name, description, max_member, current_member)
VALUES ($1, $2, $3, $4, 1);

-- name: CreateGroupLeaderRole :exec
INSERT INTO member_table(group_id, member_id, role)
VALUES ($1, $2, 'Leader');

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
INSERT INTO member_table(group_id, member_id) VALUES ($1, $2);

-- name: DeleteMemFromGroup :exec
DELETE FROM member_table WHERE group_id = $1 AND member_id = $2;

-- name: SetGroupMemberCount :exec
UPDATE chat_groups SET current_member = $2, updated_at = NOW() WHERE id = $1;
