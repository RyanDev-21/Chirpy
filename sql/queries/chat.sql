-- name: AddMessage :one
INSERT INTO message(content,parentId,from_id,to_id)VALUES(
    $1,
    $2,
    $3,
    $4
)
RETURNING *;

