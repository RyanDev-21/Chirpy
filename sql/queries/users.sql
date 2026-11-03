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
SELECT u.email, u.name, u.created_at, u.updated_at, u.id, u.is_chirpy_red, similarity(name, $1) AS similarity
FROM users u
WHERE name % $1
ORDER BY similarity DESC, name
LIMIT 20;
