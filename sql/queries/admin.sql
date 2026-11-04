-- name: SetAdminByEmail :one
UPDATE users SET is_admin = @is_admin, updated_at = NOW() WHERE email = lower(@email) RETURNING id, name;

-- name: ListAdmins :many
SELECT id, name, email FROM users WHERE is_admin ORDER BY name;

-- name: AdminOverview :one
SELECT
  (SELECT count(*) FROM users)::bigint AS users,
  (SELECT count(*) FROM users WHERE email_verified_at IS NOT NULL)::bigint AS verified,
  (SELECT count(*) FROM users WHERE created_at > @day_ago::timestamp)::bigint AS signups_24h,
  (SELECT count(*) FROM users WHERE created_at > @week_ago::timestamp)::bigint AS signups_7d,
  (SELECT count(*) FROM users WHERE last_seen_at > @week_ago::timestamp)::bigint AS active_7d,
  (SELECT count(*) FROM users WHERE banned_at IS NOT NULL)::bigint AS banned,
  ((SELECT count(*) FROM message WHERE created_at > @day_ago::timestamp)
   + (SELECT count(*) FROM GroupMessage WHERE created_at > @day_ago::timestamp))::bigint AS messages_24h,
  (SELECT count(*) FROM chat_groups)::bigint AS groups,
  (SELECT COALESCE(sum(size), 0) FROM media WHERE deleted_at IS NULL)::bigint AS media_bytes;

-- name: AdminSearchUsers :many
-- metadata only: never message content
SELECT u.id, u.name, u.display_name, u.email, (u.email_verified_at IS NOT NULL)::bool AS verified, u.created_at, u.last_seen_at,
       u.is_admin, u.banned_at, u.banned_until, u.ban_reason
FROM users u
WHERE @q::text = '' OR u.name ILIKE '%' || @q::text || '%' OR u.email ILIKE '%' || @q::text || '%'
ORDER BY u.created_at DESC
LIMIT @lim OFFSET @off;

-- name: AdminUserStats :one
SELECT
  ((SELECT count(*) FROM message WHERE from_id = @id::uuid) + (SELECT count(*) FROM GroupMessage WHERE from_id = @id::uuid))::bigint AS messages_sent,
  (SELECT count(*) FROM user_relationships WHERE status = 'confirm' AND (user_id = @id::uuid OR otherUser_id = @id::uuid))::bigint AS friends,
  (SELECT count(*) FROM member_table WHERE member_id = @id::uuid)::bigint AS groups,
  (SELECT count(*) FROM user_devices WHERE user_id = @id::uuid)::bigint AS devices;

-- name: AdminGetUser :one
SELECT id, name, display_name, email, (email_verified_at IS NOT NULL)::bool AS verified, created_at, last_seen_at,
       is_admin, banned_at, banned_until, ban_reason
FROM users WHERE id = $1;

-- name: AdminSearchGroups :many
SELECT g.id, g.name, g.current_member, g.created_at,
       COALESCE((SELECT u.name FROM member_table m JOIN users u ON u.id = m.member_id
        WHERE m.group_id = g.id AND m.role = 'Leader' LIMIT 1), '')::text AS leader,
       (SELECT count(*) FROM GroupMessage gm WHERE gm.group_id = g.id AND gm.created_at > @week_ago::timestamp)::bigint AS messages_7d
FROM chat_groups g
WHERE @q::text = '' OR g.name ILIKE '%' || @q::text || '%'
ORDER BY g.created_at DESC
LIMIT @lim OFFSET @off;

-- name: BanUser :execrows
-- admins can't be banned (revoke admin with the CLI first)
UPDATE users SET banned_at = @now::timestamp, banned_until = sqlc.narg(until), ban_reason = @reason, updated_at = @now::timestamp
WHERE id = @id AND NOT is_admin;

-- name: UnbanUser :execrows
UPDATE users SET banned_at = NULL, banned_until = NULL, ban_reason = NULL, updated_at = NOW()
WHERE id = $1 AND banned_at IS NOT NULL;

-- name: ExpiredBans :many
SELECT id FROM users WHERE banned_at IS NOT NULL AND banned_until IS NOT NULL AND banned_until < @now::timestamp;


-- name: UserBan :one
SELECT banned_at, banned_until, is_admin FROM users WHERE id = $1;

-- name: LogAdminAction :exec
INSERT INTO admin_actions (id, admin_id, action, target_user, target_group, detail, at)
VALUES (@id, sqlc.narg(admin_id), @action, sqlc.narg(target_user), sqlc.narg(target_group), @detail, @at::timestamp);

-- name: ListAdminActions :many
SELECT a.id, a.action, a.target_user, a.target_group, a.detail, a.at, COALESCE(u.name, 'cli/system') AS admin
FROM admin_actions a LEFT JOIN users u ON u.id = a.admin_id
ORDER BY a.at DESC
LIMIT @lim;

-- name: TouchLastSeen :exec
UPDATE users SET last_seen_at = @now::timestamp WHERE id = @id;

-- name: GroupMembershipsOf :many
-- groups the user is in, with their role and how many members there are
SELECT m.group_id, m.role, g.current_member FROM member_table m JOIN chat_groups g ON g.id = m.group_id
WHERE m.member_id = $1;

-- name: AllBannedUsers :many
-- the (normally short) list of banned accounts, cached by each server
SELECT id FROM users WHERE banned_at IS NOT NULL;
