-- name: PrivateMessageParticipants :one
SELECT from_id, to_id, created_at FROM message WHERE id = $1 AND deleted_at IS NULL;

-- name: GroupMessageGroup :one
SELECT group_id, from_id, created_at FROM GroupMessage WHERE id = $1 AND deleted_at IS NULL;

-- name: SetPrivateReaction :exec
-- also marks the message changed (see changed_at)
WITH t AS (UPDATE message SET changed_at = @at::timestamp WHERE id = @message_id)
INSERT INTO message_reactions (message_id, user_id, emoji, anim) VALUES (@message_id, @user_id, @emoji, @anim)
ON CONFLICT (message_id, user_id) DO UPDATE SET emoji = EXCLUDED.emoji, anim = EXCLUDED.anim, created_at = NOW();

-- name: SetGroupReaction :exec
WITH t AS (UPDATE GroupMessage SET changed_at = @at::timestamp WHERE id = @message_id)
INSERT INTO group_message_reactions (message_id, user_id, emoji, anim) VALUES (@message_id, @user_id, @emoji, @anim)
ON CONFLICT (message_id, user_id) DO UPDATE SET emoji = EXCLUDED.emoji, anim = EXCLUDED.anim, created_at = NOW();

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
-- per message, emoji and animation: count, whether @me is among them, and who
-- (pigeon reactions play each person's character)
SELECT message_id, emoji, COALESCE(anim, '')::text AS anim, count(*)::int AS n, bool_or(user_id = @me::uuid) AS mine,
       min(created_at)::timestamp AS first_at, array_agg(user_id ORDER BY created_at)::uuid[] AS user_ids
FROM message_reactions WHERE message_id = ANY(@ids::uuid[])
GROUP BY message_id, emoji, COALESCE(anim, '')
ORDER BY message_id, first_at;

-- name: GroupReactionSummary :many
SELECT message_id, emoji, COALESCE(anim, '')::text AS anim, count(*)::int AS n, bool_or(user_id = @me::uuid) AS mine,
       min(created_at)::timestamp AS first_at, array_agg(user_id ORDER BY created_at)::uuid[] AS user_ids
FROM group_message_reactions WHERE message_id = ANY(@ids::uuid[])
GROUP BY message_id, emoji, COALESCE(anim, '')
ORDER BY message_id, first_at;

-- name: PrivateReactors :many
SELECT r.user_id, u.name, u.display_name, r.emoji, COALESCE(r.anim, '')::text AS anim, r.created_at FROM message_reactions r
JOIN users u ON u.id = r.user_id
WHERE r.message_id = $1 ORDER BY r.created_at;

-- name: GroupReactors :many
SELECT r.user_id, u.name, u.display_name, r.emoji, COALESCE(r.anim, '')::text AS anim, r.created_at FROM group_message_reactions r
JOIN users u ON u.id = r.user_id
WHERE r.message_id = $1 ORDER BY r.created_at;
