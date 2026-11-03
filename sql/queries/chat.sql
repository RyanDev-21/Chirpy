-- name: AddMessagePrivate :one
INSERT INTO message(id,content,parentId,from_id,to_id,created_at)VALUES(
    $1,
    $2,
    $3,
    $4,
    $5,
    $6
)
RETURNING *;

-- name: AddMessagePublic :one
INSERT INTO GroupMessage(id,content,group_id,from_id,parent_id,created_at)
VALUES(
    $1,
    $2,
    $3,
    $4,
    $5, 
    $6
)
RETURNING *;

-- name: GetMessagesForPrivate :many
SELECT * FROM message
WHERE (from_id = $1 AND to_id = $2) OR (from_id = $2 AND to_id = $1)
ORDER BY created_at ASC;

-- name: GetMessagesForPrivateWithTime :many
SELECT * FROM message
WHERE ((from_id = $1 AND to_id = $2) OR (from_id = $2 AND to_id = $1))
  AND created_at > $3
ORDER BY created_at ASC;

-- name: GetMessagesForPublic :many
SELECT * FROM GroupMessage WHERE group_id = $1 ORDER BY created_at ASC;


-- name: AddLastSeenMessage :one
INSERT INTO seen_messages(chat_id,message_id,seen_id)
VALUES ($1,$2,$3)
ON CONFLICT (chat_id, seen_id)
DO UPDATE SET message_id = EXCLUDED.message_id, updated_time = NOW()
RETURNING *;

