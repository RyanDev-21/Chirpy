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
SELECT ur.id, ur.user_id AS from_id, u.name AS from_name
FROM user_relationships ur
JOIN users u ON u.id = ur.user_id
WHERE ur.otherUser_id = $1 AND ur.status = 'pending'
ORDER BY ur.created_at;

-- name: ListOutgoingFriendRequests :many
SELECT ur.id, ur.otherUser_id AS to_id, u.name AS to_name
FROM user_relationships ur
JOIN users u ON u.id = ur.otherUser_id
WHERE ur.user_id = $1 AND ur.status = 'pending'
ORDER BY ur.created_at;

-- name: ListFriends :many
SELECT u.id AS friend_id, u.name AS friend_name
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
