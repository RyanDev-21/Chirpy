-- name: CreateRecord :one
INSERT INTO chirp(id,created_at,updated_at,body,user_id)
VALUES(
    gen_random_uuid(),
    NOW(),
    NOW(),
    $1,
    $2
)
RETURNING *;

-- name: GetAllRecord :many
SELECT * FROM chirp;


-- name: GetRecordByID :one
SELECT * FROM chirp WHERE id = $1;

-- name: DeleteRecords :exec
DELETE FROM chirp;
