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
SELECT id,current_member,name,max_member FROM chat_groups;
-- name: GetMemberListByID :many
SELECT member_id FROM member_table WHERE group_id = $1;

-- name: UpdateGroupCurrentMemberByID :one
UPDATE  chat_groups SET current_member = (SELECT COUNT(*)FROM member_table WHERE group_id = $1)RETURNING *;


-- name: AddMemberList :copyfrom
INSERT INTO member_table(group_id,member_id) VALUES($1,$2);


-- name: AddMember :exec
INSERT INTO member_table(group_id,member_id) VALUES($1,$2);


