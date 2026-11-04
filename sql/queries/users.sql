-- name: CreateUser :one
INSERT INTO users(id, name, created_at, updated_at, email, password)
VALUES (gen_random_uuid(), $1, NOW(), NOW(), $2, $3)
RETURNING *;

-- name: GetUserInfoByEmail :one
SELECT * FROM users WHERE email = $1;

-- name: GetUserInfoByID :one
SELECT * FROM users WHERE id = $1;

-- name: DeleteUser :exec
DELETE FROM users;

-- name: UpdatePassword :exec
UPDATE users SET password = $1, updated_at = NOW() WHERE id = $2;

-- name: SearchNameSiml :many
-- people who blocked the caller, or whom the caller blocked, are left out
SELECT u.id, u.name, similarity(u.name, @q::text) AS similarity
FROM users u
WHERE u.name % @q::text
  AND NOT EXISTS (
      SELECT 1 FROM user_blocks b
      WHERE (b.blocker_id = u.id AND b.blocked_id = @me::uuid)
         OR (b.blocker_id = @me::uuid AND b.blocked_id = u.id)
  )
ORDER BY similarity DESC, u.name
LIMIT 20;

-- name: RenameUser :one
-- only if the last rename is older than the cutoff (checked again here so
-- two concurrent renames can't both pass)
UPDATE users SET name = @name, name_changed_at = @now::timestamp, updated_at = @now::timestamp
WHERE id = @id AND (name_changed_at IS NULL OR name_changed_at < @cutoff::timestamp)
RETURNING *;

-- name: SetBio :one
UPDATE users SET bio = @bio, updated_at = NOW() WHERE id = @id RETURNING *;

-- name: GetPublicProfile :one
-- hidden (no row) when either user blocked the other
SELECT u.id, u.name, u.bio FROM users u
WHERE u.id = @id
  AND NOT EXISTS (
      SELECT 1 FROM user_blocks b
      WHERE (b.blocker_id = u.id AND b.blocked_id = @viewer::uuid)
         OR (b.blocker_id = @viewer::uuid AND b.blocked_id = u.id)
  );
