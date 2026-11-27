-- name: CreateStory :exec
INSERT INTO stories (id, user_id, created_at, expires_at, stickers, repost_of) VALUES (@id, @user_id, @now::timestamp, @expires_at::timestamp, @stickers, @repost_of);

-- name: CountActiveStories :one
SELECT count(*)::int FROM stories WHERE user_id = @user_id AND expires_at > @now::timestamp;

-- name: MyStories :many
SELECT s.id, s.created_at, s.expires_at, s.stickers, (SELECT count(*) FROM story_views v WHERE v.story_id = s.id)::int AS views,
       s.repost_of, ou.id AS orig_user_id, COALESCE(ou.name, '')::text AS orig_name, COALESCE(ou.display_name, '')::text AS orig_display
FROM stories s
LEFT JOIN stories os ON os.id = s.repost_of
LEFT JOIN users ou ON ou.id = os.user_id
WHERE s.user_id = @me::uuid AND s.expires_at > @now::timestamp
ORDER BY s.created_at, s.id;

-- name: FriendStories :many
-- my friends' live stories (not muted by me, not from suspended accounts),
-- oldest first per person, and whether I've seen each
SELECT s.id, s.user_id, u.name, u.display_name, u.avatar_at, u.character, u.character_v, s.created_at, s.expires_at, s.stickers,
       EXISTS (SELECT 1 FROM story_views v WHERE v.story_id = s.id AND v.viewer_id = @me::uuid)::bool AS seen,
       s.repost_of, ou.id AS orig_user_id, COALESCE(ou.name, '')::text AS orig_name, COALESCE(ou.display_name, '')::text AS orig_display
FROM stories s
JOIN users u ON u.id = s.user_id
LEFT JOIN stories os ON os.id = s.repost_of
LEFT JOIN users ou ON ou.id = os.user_id
WHERE s.expires_at > @now::timestamp
  AND u.banned_at IS NULL
  AND s.user_id IN (
      SELECT CASE WHEN r.user_id = @me::uuid THEN r.otherUser_id ELSE r.user_id END
      FROM user_relationships r
      WHERE r.status = 'confirm' AND (r.user_id = @me::uuid OR r.otherUser_id = @me::uuid))
  AND s.user_id NOT IN (SELECT m.muted_id FROM story_mutes m WHERE m.user_id = @me::uuid)
ORDER BY s.user_id, s.created_at, s.id;

-- name: VisibleStoryOwner :one
-- the owner of a live story @viewer may see: theirs, or a friend's (not suspended)
SELECT s.user_id FROM stories s
JOIN users u ON u.id = s.user_id
WHERE s.id = @id AND s.expires_at > @now::timestamp AND u.banned_at IS NULL
  AND (s.user_id = @viewer::uuid OR EXISTS (
      SELECT 1 FROM user_relationships r
      WHERE r.status = 'confirm'
        AND ((r.user_id = s.user_id AND r.otherUser_id = @viewer::uuid) OR (r.user_id = @viewer::uuid AND r.otherUser_id = s.user_id))));

-- name: AddStoryView :exec
INSERT INTO story_views (story_id, viewer_id, viewed_at) VALUES (@story_id, @viewer_id, @now::timestamp)
ON CONFLICT DO NOTHING;

-- name: StoryViewers :many
-- each viewer, and their latest reply to this story (the app shows it as a
-- reaction when it's one emoji or an animation)
SELECT u.id, u.name, u.display_name, u.avatar_at, v.viewed_at,
       COALESCE(r.content, '')::text AS reply_text, COALESCE(r.anim, '')::text AS reply_anim
FROM story_views v
JOIN stories s ON s.id = v.story_id
JOIN users u ON u.id = v.viewer_id
LEFT JOIN LATERAL (
    SELECT m.content, m.anim FROM message m
    WHERE m.story_id = v.story_id AND m.from_id = v.viewer_id AND m.to_id = s.user_id AND m.deleted_at IS NULL
    ORDER BY m.created_at DESC LIMIT 1
) r ON true
WHERE v.story_id = @story_id AND s.user_id = @owner::uuid
ORDER BY v.viewed_at DESC;

-- name: StoryOwnedBy :one
SELECT EXISTS (SELECT 1 FROM stories WHERE id = @id AND user_id = @owner::uuid)::bool;

-- name: DeleteStory :execrows
DELETE FROM stories WHERE id = @id AND user_id = @owner::uuid;

-- name: MuteStories :exec
INSERT INTO story_mutes (user_id, muted_id, created_at) VALUES (@user_id, @muted_id, @now::timestamp)
ON CONFLICT DO NOTHING;

-- name: UnmuteStories :exec
DELETE FROM story_mutes WHERE user_id = @user_id AND muted_id = @muted_id;

-- name: StoryMutes :many
SELECT u.id, u.name, u.display_name, u.avatar_at
FROM story_mutes m JOIN users u ON u.id = m.muted_id
WHERE m.user_id = @user_id
ORDER BY m.created_at DESC;

-- name: DeleteExpiredStories :execrows
DELETE FROM stories WHERE expires_at <= @now::timestamp;

-- name: LiveStoryOwner :one
-- for a reply: the owner of a story that hasn't expired
SELECT user_id FROM stories WHERE id = @id AND expires_at > @now::timestamp;

-- name: FriendsAmong :many
-- which of @ids are @me's friends
SELECT CASE WHEN r.user_id = @me::uuid THEN r.otherUser_id ELSE r.user_id END::uuid AS id
FROM user_relationships r
WHERE r.status = 'confirm'
  AND ((r.user_id = @me::uuid AND r.otherUser_id = ANY(@ids::uuid[])) OR (r.otherUser_id = @me::uuid AND r.user_id = ANY(@ids::uuid[])));

-- name: AddStoryMention :exec
INSERT INTO story_mentions (story_id, user_id) VALUES (@story_id, @user_id) ON CONFLICT DO NOTHING;

-- name: UserNames :many
SELECT id, name, display_name FROM users WHERE id = ANY(@ids::uuid[]);

-- name: RepostSource :one
-- a live original story that mentions @me, and its photo
SELECT s.id, s.user_id, s.expires_at, m.id AS media_id
FROM stories s
JOIN story_mentions sm ON sm.story_id = s.id AND sm.user_id = @me::uuid
JOIN media m ON m.message_id = s.id AND m.chat_type = 'story' AND m.deleted_at IS NULL
WHERE s.id = @id AND s.repost_of IS NULL AND s.expires_at > @now::timestamp;
