-- name: SavePosition :one
INSERT INTO EleConfig(user_id,pref)
VALUES($1,$2)RETURNING *;


-- name: GetAllConfigForUser :one
SELECT pref FROM EleConfig WHERE user_id = $1;

-- name: GetAllUsersConfig :many
SELECT * FROM EleConfig ;

