-- name: CreateFriendRequest :exec
INSERT INTO user_relationships (id, user_id, otherUser_id)
VALUES ($1, $2, $3);

-- name: PairStatus :one
SELECT status FROM user_relationships
WHERE LEAST(user_id, otherUser_id) = LEAST(@a::uuid, @b::uuid)
  AND GREATEST(user_id, otherUser_id) = GREATEST(@a::uuid, @b::uuid);

-- name: AcceptFriendRequest :one
UPDATE user_relationships
SET status = 'confirm', updated_at = NOW()
WHERE id = $1 AND otherUser_id = $2 AND status = 'pending'
RETURNING user_id;

-- name: DenyFriendRequest :one
DELETE FROM user_relationships
WHERE id = $1 AND otherUser_id = $2 AND status = 'pending'
RETURNING user_id;

-- name: CancelFriendRequest :one
DELETE FROM user_relationships
WHERE id = $1 AND user_id = $2 AND status = 'pending'
RETURNING otherUser_id;

-- name: ListIncomingFriendRequests :many
SELECT ur.id, ur.user_id AS from_id, u.name AS from_name, u.avatar_at AS from_avatar_at
FROM user_relationships ur
JOIN users u ON u.id = ur.user_id
WHERE ur.otherUser_id = $1 AND ur.status = 'pending'
ORDER BY ur.created_at;

-- name: ListOutgoingFriendRequests :many
SELECT ur.id, ur.otherUser_id AS to_id, u.name AS to_name, u.avatar_at AS to_avatar_at
FROM user_relationships ur
JOIN users u ON u.id = ur.otherUser_id
WHERE ur.user_id = $1 AND ur.status = 'pending'
ORDER BY ur.created_at;

-- name: ListFriends :many
SELECT u.id AS friend_id, u.name AS friend_name, u.avatar_at AS friend_avatar_at
FROM users u
WHERE u.id IN (
    SELECT r1.otherUser_id FROM user_relationships r1 WHERE r1.user_id = $1 AND r1.status = 'confirm'
    UNION
    SELECT r2.user_id FROM user_relationships r2 WHERE r2.otherUser_id = $1 AND r2.status = 'confirm'
)
ORDER BY u.name;

-- name: Unfriend :one
DELETE FROM user_relationships
WHERE status = 'confirm'
  AND LEAST(user_id, otherUser_id) = LEAST(@a::uuid, @b::uuid)
  AND GREATEST(user_id, otherUser_id) = GREATEST(@a::uuid, @b::uuid)
RETURNING id;

-- name: BlockUser :one
-- records the block and removes any friendship or request between the
-- two; returns whether they were friends
WITH b AS (
    INSERT INTO user_blocks (blocker_id, blocked_id, created_at) VALUES (@blocker::uuid, @blocked::uuid, @at::timestamp)
    ON CONFLICT DO NOTHING
), d AS (
    DELETE FROM user_relationships
    WHERE LEAST(user_id, otherUser_id) = LEAST(@blocker::uuid, @blocked::uuid)
      AND GREATEST(user_id, otherUser_id) = GREATEST(@blocker::uuid, @blocked::uuid)
    RETURNING status
)
SELECT COALESCE(bool_or(status = 'confirm'), false)::bool AS were_friends FROM d;

-- name: UnblockUser :exec
DELETE FROM user_blocks WHERE blocker_id = $1 AND blocked_id = $2;

-- name: ListBlocked :many
SELECT b.blocked_id, u.name, b.created_at FROM user_blocks b
JOIN users u ON u.id = b.blocked_id
WHERE b.blocker_id = $1
ORDER BY b.created_at DESC;

-- name: BlockBetween :one
-- whether a blocked b (a_blocked) and whether b blocked a (b_blocked)
SELECT
    EXISTS (SELECT 1 FROM user_blocks WHERE blocker_id = @a::uuid AND blocked_id = @b::uuid) AS a_blocked,
    EXISTS (SELECT 1 FROM user_blocks WHERE blocker_id = @b::uuid AND blocked_id = @a::uuid) AS b_blocked;
