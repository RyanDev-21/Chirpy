-- name: CreateUser :one
INSERT INTO users(id, name, display_name, created_at, updated_at, email, password)
VALUES (gen_random_uuid(), $1, $4, NOW(), NOW(), $2, $3)
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
-- people who blocked the caller, or whom the caller blocked, are left out
SELECT u.id, u.name, u.display_name, u.avatar_at,
       greatest(similarity(u.name, @q::text), similarity(u.display_name, @q::text))::real AS similarity
FROM users u
WHERE (u.name % @q::text OR u.display_name % @q::text)
  AND u.banned_at IS NULL
  AND NOT EXISTS (
      SELECT 1 FROM user_blocks b
      WHERE (b.blocker_id = u.id AND b.blocked_id = @me::uuid)
         OR (b.blocker_id = @me::uuid AND b.blocked_id = u.id)
  )
ORDER BY similarity DESC, u.name
LIMIT 20;

-- name: RenameUser :one
-- only if the last rename is older than the cutoff (checked again here so
-- two concurrent renames can't both pass)
UPDATE users SET name = @name, name_changed_at = @now::timestamp, updated_at = @now::timestamp
WHERE id = @id AND (name_changed_at IS NULL OR name_changed_at < @cutoff::timestamp)
RETURNING *;

-- name: SetBio :one
UPDATE users SET bio = @bio, updated_at = NOW() WHERE id = @id RETURNING *;

-- name: GetPublicProfile :one
-- hidden (no row) when either user blocked the other
SELECT u.id, u.name, u.display_name, u.bio, u.avatar_at, u.location_id, u.discoverable, u.character, u.character_v FROM users u
WHERE u.id = @id
  AND u.banned_at IS NULL
  AND NOT EXISTS (
      SELECT 1 FROM user_blocks b
      WHERE (b.blocker_id = u.id AND b.blocked_id = @viewer::uuid)
         OR (b.blocker_id = @viewer::uuid AND b.blocked_id = u.id)
  );

-- name: SetAvatar :exec
WITH a AS (
    INSERT INTO user_avatars (user_id, image, updated_at) VALUES (@user_id, @image, @at::timestamp)
    ON CONFLICT (user_id) DO UPDATE SET image = EXCLUDED.image, updated_at = EXCLUDED.updated_at
)
UPDATE users SET avatar_at = @at::timestamp WHERE id = @user_id;

-- name: DeleteAvatar :exec
WITH a AS (DELETE FROM user_avatars WHERE user_id = @user_id)
UPDATE users SET avatar_at = NULL WHERE id = @user_id;

-- name: GetAvatar :one
-- hidden (no row) when either user blocked the other
SELECT a.image, a.updated_at FROM user_avatars a
WHERE a.user_id = @id
  AND NOT EXISTS (
      SELECT 1 FROM user_blocks b
      WHERE (b.blocker_id = a.user_id AND b.blocked_id = @viewer::uuid)
         OR (b.blocker_id = @viewer::uuid AND b.blocked_id = a.user_id)
  );

-- name: SetLocation :exec
UPDATE users SET location_id = sqlc.narg(location_id), location_country = @country, updated_at = NOW() WHERE id = @id;

-- name: SetDiscoverable :exec
UPDATE users SET discoverable = @discoverable, updated_at = NOW() WHERE id = @id;

-- name: RemoveOtherInterests :exec
DELETE FROM user_interests WHERE user_id = @user_id AND NOT (interest_id = ANY(@keep::text[]));

-- name: AddInterests :exec
INSERT INTO user_interests (user_id, interest_id)
SELECT @user_id, unnest(@ids::text[])
ON CONFLICT DO NOTHING;

-- name: GetInterests :many
SELECT interest_id FROM user_interests WHERE user_id = $1 ORDER BY interest_id;

-- name: AreFriendsSQL :one
SELECT EXISTS (
    SELECT 1 FROM user_relationships
    WHERE status = 'confirm'
      AND LEAST(user_id, otherUser_id) = LEAST(@a::uuid, @b::uuid)
      AND GREATEST(user_id, otherUser_id) = GREATEST(@a::uuid, @b::uuid)
);

-- name: SetDisplayName :exec
UPDATE users SET display_name = @display_name, updated_at = NOW() WHERE id = @id;

-- name: GetDisplayName :one
SELECT CASE WHEN display_name = '' THEN name ELSE display_name END::text FROM users WHERE id = $1;

-- name: SetHideLastSeen :exec
UPDATE users SET hide_last_seen = @hide, updated_at = NOW() WHERE id = @id;

-- name: PresenceOf :many
SELECT id, last_seen_at, hide_last_seen FROM users WHERE id = ANY(@ids::uuid[]);

-- name: MutualFriends :many
-- Friends @a and @b have in common: the first @lim by name, each row
-- carrying the total.
WITH fa AS (
    SELECT CASE WHEN user_id = @a::uuid THEN otherUser_id ELSE user_id END AS fid
    FROM user_relationships
    WHERE status = 'confirm' AND (user_id = @a::uuid OR otherUser_id = @a::uuid)
), fb AS (
    SELECT CASE WHEN user_id = @b::uuid THEN otherUser_id ELSE user_id END AS fid
    FROM user_relationships
    WHERE status = 'confirm' AND (user_id = @b::uuid OR otherUser_id = @b::uuid)
)
SELECT u.id, u.name, u.display_name, u.avatar_at, (count(*) OVER ())::int AS total
FROM fa JOIN fb ON fb.fid = fa.fid
JOIN users u ON u.id = fa.fid
WHERE u.banned_at IS NULL
ORDER BY u.name
LIMIT @lim;

-- name: SetHideStreaks :exec
UPDATE users SET hide_streaks = @hide, updated_at = NOW() WHERE id = @id;

-- name: GetPrivacy :one
SELECT hide_last_seen, hide_streaks FROM users WHERE id = @id;

-- name: SetNote :one
INSERT INTO notes (user_id, text, created_at, expires_at) VALUES (@user_id, @text, @now::timestamp, @expires_at::timestamp)
ON CONFLICT (user_id) DO UPDATE SET text = EXCLUDED.text, created_at = EXCLUDED.created_at, expires_at = EXCLUDED.expires_at
RETURNING user_id, text, created_at, expires_at;

-- name: DeleteNote :exec
DELETE FROM notes WHERE user_id = @user_id;

-- name: FriendNotes :many
-- my note and my friends' notes that haven't expired (not suspended users)
SELECT n.user_id, u.name, u.display_name, u.avatar_at, u.character, u.character_v, n.text, n.created_at, n.expires_at
FROM notes n
JOIN users u ON u.id = n.user_id
WHERE n.expires_at > @now::timestamp
  AND u.banned_at IS NULL
  AND (n.user_id = @me::uuid OR n.user_id IN (
      SELECT CASE WHEN r.user_id = @me::uuid THEN r.otherUser_id ELSE r.user_id END
      FROM user_relationships r
      WHERE r.status = 'confirm' AND (r.user_id = @me::uuid OR r.otherUser_id = @me::uuid)))
ORDER BY (n.user_id = @me::uuid) DESC, n.created_at DESC;

-- name: DeleteExpiredNotes :execrows
DELETE FROM notes WHERE expires_at <= @now::timestamp;

-- name: UpgradePasswordHash :execrows
-- same password, newer hash settings; only if it wasn't changed meanwhile
UPDATE users SET password = @new_hash WHERE id = @id AND password = @old_hash;

-- name: SetCharacter :exec
UPDATE users SET character = @character::jsonb, character_v = @version, updated_at = NOW() WHERE id = @id;
