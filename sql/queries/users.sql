-- name: CreateUser :one
INSERT INTO  users(id,name,created_at,updated_at, email,password)
VALUES(
    gen_random_uuid(),
    $1,
    NOW(),
    NOW(),
    $2,
    $3
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

-- name: UpdateIsRedById :execresult
UPDATE users SET  is_chirpy_red = true ,updated_at = NOW() WHERE id = $1;

-- name: GetAllUser :many
SELECT * FROM users;


-- name: GetAllUserRs :many
SELECT * FROM user_relationships WHERE status != 'pending';

-- name: AddSendReq :exec
INSERT INTO user_relationships (id,user_id,otherUser_id)
VALUES(
    $1,
    $2,
    $3
);

-- name: UpdateSendReq :exec
UPDATE user_relationships SET status = 'confirm',updated_at = NOW() WHERE id = $1;
-- name: GetFriReqList :many
SELECT *  FROM user_relationships WHERE otherUser_id = $1 AND status != 'confirm';

-- name: GetYourSendReqList :many
SELECT * FROM user_relationships WHERE user_id = $1 AND status!= 'confirm';

-- name: GetUserFriListByID :many
SELECT 
    CASE 
    WHEN user_id = $1 THEN otherUser_id
    WHEN otherUser_id = $1 THEN user_id
    END AS friend_id 
FROM user_relationships WHERE status == 'confirm';


-- name: CancelFriReqStatus :exec
UPDATE user_relationships SET status = 'cancel',updated_at = $2 WHERE id = $1;

-- name: DeleteFriReq :exec
DELETE FROM user_relationships WHERE id=$1;
