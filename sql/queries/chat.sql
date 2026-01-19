-- name: AddMessagePrivate :one
INSERT INTO message(id,content,parentId,from_id,to_id)VALUES(
    $1,
    $2,
    $3,
    $4,
    $5
)
RETURNING *;

-- name: AddMessagePublic :one
INSERT INTO GroupMessage(id,content,group_id,from_id,parent_id)
VALUES(
    $1,
    $2,
    $3,
    $4,
    $5
)
RETURNING *;

