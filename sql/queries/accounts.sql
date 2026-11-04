-- name: MarkEmailVerified :exec
UPDATE users SET email_verified_at = COALESCE(email_verified_at, NOW()), updated_at = NOW() WHERE id = $1;

-- name: SetPasswordHash :exec
UPDATE users SET password = $2, updated_at = NOW() WHERE id = $1;

-- name: UpsertEmailCode :exec
-- created_at is the server clock (compared with it for the resend gap)
INSERT INTO email_codes (user_id, purpose, code_hash, expires_at, created_at)
VALUES ($1, $2, $3, $4, @created_at::timestamp)
ON CONFLICT (user_id, purpose)
DO UPDATE SET code_hash = EXCLUDED.code_hash, attempts = 0, expires_at = EXCLUDED.expires_at, created_at = EXCLUDED.created_at;

-- name: GetEmailCode :one
SELECT * FROM email_codes WHERE user_id = $1 AND purpose = $2;

-- name: BumpEmailCodeAttempts :one
UPDATE email_codes SET attempts = attempts + 1 WHERE user_id = $1 AND purpose = $2 RETURNING attempts;

-- name: DeleteEmailCode :exec
DELETE FROM email_codes WHERE user_id = $1 AND purpose = $2;

-- name: GetIdentity :one
SELECT * FROM user_identities WHERE provider = $1 AND subject = $2;

-- name: CreateIdentity :exec
INSERT INTO user_identities (provider, subject, user_id, email) VALUES ($1, $2, $3, $4);

-- name: CreateUserVerifiedNoPassword :one
INSERT INTO users (id, name, display_name, created_at, updated_at, email, password, email_verified_at)
VALUES (gen_random_uuid(), $1, $3, NOW(), NOW(), $2, NULL, NOW())
RETURNING *;

-- name: GroupsLedBy :many
SELECT group_id FROM member_table WHERE member_id = $1 AND role = 'Leader';

-- name: EarliestOtherMember :one
SELECT member_id FROM member_table
WHERE group_id = $1 AND member_id <> $2
ORDER BY joined_at, member_id
LIMIT 1;

-- name: PromoteToLeader :exec
UPDATE member_table SET role = 'Leader' WHERE group_id = $1 AND member_id = $2;

-- name: DeleteGroup :exec
DELETE FROM chat_groups WHERE id = $1;

-- name: GroupsOfUser :many
SELECT group_id FROM member_table WHERE member_id = $1;

-- name: FriendIDsOf :many
SELECT CASE WHEN user_id = $1 THEN otherUser_id ELSE user_id END::uuid AS other_id
FROM user_relationships WHERE user_id = $1 OR otherUser_id = $1;

-- name: DeleteUserByID :exec
DELETE FROM users WHERE id = $1;

-- name: RecountGroupMembers :exec
UPDATE chat_groups
SET current_member = (SELECT count(*) FROM member_table WHERE group_id = $1), updated_at = NOW()
WHERE id = $1;

-- name: DeleteExpiredEmailCodes :execrows
DELETE FROM email_codes WHERE expires_at < @now::timestamp;
