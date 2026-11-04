-- name: PrivateMessageParticipants :one
SELECT from_id, to_id, created_at FROM message WHERE id = $1 AND deleted_at IS NULL;

-- name: GroupMessageGroup :one
SELECT group_id, from_id, created_at FROM GroupMessage WHERE id = $1 AND deleted_at IS NULL;

-- name: SetPrivateReaction :exec
INSERT INTO message_reactions (message_id, user_id, emoji) VALUES ($1, $2, $3)
ON CONFLICT (message_id, user_id) DO UPDATE SET emoji = EXCLUDED.emoji, created_at = NOW();

-- name: SetGroupReaction :exec
INSERT INTO group_message_reactions (message_id, user_id, emoji) VALUES ($1, $2, $3)
ON CONFLICT (message_id, user_id) DO UPDATE SET emoji = EXCLUDED.emoji, created_at = NOW();

-- name: DeletePrivateReaction :execrows
DELETE FROM message_reactions WHERE message_id = $1 AND user_id = $2;

-- name: DeleteGroupReaction :execrows
DELETE FROM group_message_reactions WHERE message_id = $1 AND user_id = $2;

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
