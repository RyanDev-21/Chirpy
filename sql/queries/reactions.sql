-- name: PrivateMessageParticipants :one
SELECT from_id, to_id, created_at FROM message WHERE id = $1 AND deleted_at IS NULL;

-- name: GroupMessageGroup :one
SELECT group_id, from_id, created_at FROM GroupMessage WHERE id = $1 AND deleted_at IS NULL;

-- name: SetPrivateReaction :exec
-- also marks the message changed (see changed_at)
WITH t AS (UPDATE message SET changed_at = @at::timestamp WHERE id = @message_id)
INSERT INTO message_reactions (message_id, user_id, emoji) VALUES (@message_id, @user_id, @emoji)
ON CONFLICT (message_id, user_id) DO UPDATE SET emoji = EXCLUDED.emoji, created_at = NOW();

-- name: SetGroupReaction :exec
WITH t AS (UPDATE GroupMessage SET changed_at = @at::timestamp WHERE id = @message_id)
INSERT INTO group_message_reactions (message_id, user_id, emoji) VALUES (@message_id, @user_id, @emoji)
ON CONFLICT (message_id, user_id) DO UPDATE SET emoji = EXCLUDED.emoji, created_at = NOW();

-- name: DeletePrivateReaction :one
WITH d AS (
    DELETE FROM message_reactions WHERE message_id = @message_id AND user_id = @user_id RETURNING 1
), t AS (
    UPDATE message SET changed_at = @at::timestamp WHERE id = @message_id AND EXISTS (SELECT 1 FROM d)
)
SELECT count(*)::int FROM d;

-- name: DeleteGroupReaction :one
WITH d AS (
    DELETE FROM group_message_reactions WHERE message_id = @message_id AND user_id = @user_id RETURNING 1
), t AS (
    UPDATE GroupMessage SET changed_at = @at::timestamp WHERE id = @message_id AND EXISTS (SELECT 1 FROM d)
)
SELECT count(*)::int FROM d;

-- name: PrivateReactionSummary :many
-- per message and emoji: count, and whether @me is among them
SELECT message_id, emoji, count(*)::int AS n, bool_or(user_id = @me::uuid) AS mine, min(created_at)::timestamp AS first_at
FROM message_reactions WHERE message_id = ANY(@ids::uuid[])
GROUP BY message_id, emoji
ORDER BY message_id, first_at;

-- name: GroupReactionSummary :many
SELECT message_id, emoji, count(*)::int AS n, bool_or(user_id = @me::uuid) AS mine, min(created_at)::timestamp AS first_at
FROM group_message_reactions WHERE message_id = ANY(@ids::uuid[])
GROUP BY message_id, emoji
ORDER BY message_id, first_at;

-- name: PrivateReactors :many
SELECT r.user_id, u.name, u.display_name, r.emoji, r.created_at FROM message_reactions r
JOIN users u ON u.id = r.user_id
WHERE r.message_id = $1 ORDER BY r.created_at;

-- name: GroupReactors :many
SELECT r.user_id, u.name, u.display_name, r.emoji, r.created_at FROM group_message_reactions r
JOIN users u ON u.id = r.user_id
WHERE r.message_id = $1 ORDER BY r.created_at;
