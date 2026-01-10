-- name: CreateGroup :one
INSERT INTO chat_groups(id,name,description,max_member)
VALUES(
    $1,
    $2,
    $3,
    $4
)
RETURNING *;
