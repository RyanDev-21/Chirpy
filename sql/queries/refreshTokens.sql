-- name: CreateARefreshToken :one
INSERT INTO refresh_tokens (token,created_at,updated_at,user_id,expire_at,revoked_at)
VALUES(
    $1,
    NOW(),
    NOW(),
    $2,
    $3,
    Null
)
RETURNING *;

-- name: GetRefreshToken :one
SELECT user_id,expire_at FROM refresh_tokens WHERE token = $1;

-- name: RevokeRefreshToken :exec
UPDATE refresh_tokens SET revoked_at = NOW(),updated_at = NOW() WHERE token = $1;

