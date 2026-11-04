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
