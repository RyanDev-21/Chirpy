-- name: SavePosition :exec
INSERT INTO EleConfig(user_id,pref)
VALUES($1,$2::jsonb)
ON CONFLICT (user_id)
DO UPDATE SET pref = EXCLUDED.pref::jsonb;

-- name: GetAllConfigForUser :one
SELECT pref FROM EleConfig WHERE user_id = $1;

-- name: GetAllUsersConfig :many
SELECT * FROM EleConfig ;

