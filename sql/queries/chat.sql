-- name: AddMessagePrivate :exec
INSERT INTO message(id, content, parentId, from_id, to_id, created_at)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: AddMessagePublic :exec
INSERT INTO GroupMessage(id, content, group_id, from_id, parent_id, created_at)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: GetRecentPrivateMessages :many
SELECT * FROM message
WHERE ((from_id = @user_a AND to_id = @user_b) OR (from_id = @user_b AND to_id = @user_a))
  AND deleted_at IS NULL
ORDER BY created_at DESC
LIMIT @lim;

-- name: GetPrivateMessagesSince :many
SELECT * FROM message
WHERE ((from_id = @user_a AND to_id = @user_b) OR (from_id = @user_b AND to_id = @user_a))
  AND created_at > @since
  AND deleted_at IS NULL
ORDER BY created_at ASC
LIMIT @lim;

-- name: GetRecentGroupMessages :many
SELECT * FROM GroupMessage
WHERE group_id = @group_id AND deleted_at IS NULL
ORDER BY created_at DESC
LIMIT @lim;

-- name: AddLastSeenMessage :exec
INSERT INTO seen_messages(chat_id, message_id, seen_id)
VALUES ($1, $2, $3)
ON CONFLICT (chat_id, seen_id)
DO UPDATE SET message_id = EXCLUDED.message_id, updated_time = NOW();

-- name: PrivateMessageInChat :one
SELECT EXISTS (
    SELECT 1 FROM message
    WHERE id = @id
      AND ((from_id = @user_a AND to_id = @user_b) OR (from_id = @user_b AND to_id = @user_a))
);

-- name: GroupMessageInGroup :one
SELECT EXISTS (SELECT 1 FROM GroupMessage WHERE id = @id AND group_id = @group_id);
