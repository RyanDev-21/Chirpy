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

-- name: GetMessagesForPrivate :many
SELECT * FROM message WHERE from_id = $1 AND  to_id = $2 OR from_id = $2 AND to_id= $1;


-- name: GetMessagesForPublic :many
SELECT * FROM GroupMessage WHERE group_id = $1;

-- name: GetMessagesForAllPrivateChats :many
SELECT * FROM message;

-- name: GetMessagesForAllPublicChats :many
SELECT * FROM GroupMessage;
