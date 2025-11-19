-- name: CreateUser :one
INSERT INTO  users(id, created_at,updated_at, email,password)
VALUES(
    gen_random_uuid(),
    NOW(),
    NOW(),
    $1,
    $2
)
RETURNING *;

-- name: GetUserInfoByEmail :one
SELECT *  FROM users WHERE email = $1;

-- name: DeleteUser :exec
DELETE FROM users;

-- name: UpdatePassword :exec
UPDATE  users SET password = $1,updated_at = NOW() WHERE id = $2;

-- name: GetUserInfoByID :one
SELECT * FROM users WHERE id = $1;
