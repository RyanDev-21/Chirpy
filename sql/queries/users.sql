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
SELECT ur.*,u.name FROM user_relationships ur LEFT JOIN users  u ON ur.otherUser_id = u.id  WHERE status != 'pending';

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
SELECT ur.id,ur.user_id,u.name  FROM user_relationships ur  LEFT JOIN users u ON u.id = ur.user_id WHERE ur.otherUser_id = $1 AND ur.status != 'confirm';

-- name: GetYourSendReqList :many
SELECT ur.id,ur.otherUser_id,u.name FROM user_relationships ur LEFT JOIN users u ON u.id = ur.otherUser_id WHERE ur.user_id = $1 AND ur.status!= 'confirm' ;
--
-- -- name: GetUserFriListByID :many
-- SELECT 
--     CASE 
--     WHEN user_id = $1 THEN otherUser_id
--     WHEN otherUser_id = $1 THEN user_id
--     END AS friend_id 
-- FROM user_relationships WHERE status = 'confirm';
--

-- name: GetUserFriListByID :many 
SELECT 
	ur.id,
	ur.otherUser_id,
	u.name
FROM user_relationships ur LEFT JOIN 
users u ON u.id = (
	CASE 
	WHEN ur.user_id = $1 THEN ur.otherUser_id
	WHEN ur.otherUser_id = $1 THEN ur.user_id
	END
)
WHERE ur.status = 'confirm' AND ($1 IN (ur.user_id,ur.otherUser_id));


-- name: CancelFriReqStatus :exec
UPDATE user_relationships SET status = 'cancel',updated_at = $2 WHERE id = $1;

-- name: DeleteFriReq :exec
DELETE FROM user_relationships WHERE id=$1;

-- name: GetOtherUserInfoByReqID :one
SELECT 
	u.id,
	u.email,
	u.name,
	u.created_at,
	u.updated_at
FROM user_relationships ur LEFT JOIN
users u ON u.id  = (
	CASE 
		WHEN ur.user_id = $1 THEN ur.otherUser_id
		WHEN ur.otherUser_id = $1 THEN ur.user_id
	END
)
WHERE ur.id = $2;

-- name: SearchNameSiml :many
SELECT u.email,u.name,u.created_at,u.updated_at,u.id,u.is_chirpy_red,similarity(name,$1) AS similarity
FROM users u
WHERE name % $1
ORDER BY similarity DESC,name;
