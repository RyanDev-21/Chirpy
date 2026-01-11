-- name: CreateGroup :one
INSERT INTO chat_groups(id,name,description,max_member)
VALUES(
    $1,
    $2,
    $3,
    $4
)
RETURNING *;

-- name: JoinGroup :one
INSERT INTO member_table(group_id,member_id)
VALUES(
    $1,
    $2
)
RETURNING *;

-- name: CreateGroupLeaderRole :one
INSERT INTO member_table(group_id,member_id,role)
VALUES(
    $1,
    $2,
    $3
)
RETURNING *;

-- name: SearchInfoByName :one
SELECT * FROM chat_groups WHERE name = $1;

-- name: GetGroupInfoByID :one
SELECT * FROM chat_groups WHERE  id = $1;

-- name: GetAllGroupInfo :many
SELECT id,name,max_member,current_member FROM chat_groups;


-- name: GetTotalMemberCountByID :one
SELECT COUNT(*)FROM member_table WHERE group_id = $1;
