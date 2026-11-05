-- name: CreateMedia :exec
INSERT INTO media (id, owner_id, kind, content_type, size, created_at, expires_at, last_access_at, duration_ms, width, height, file_name)
VALUES (@id, @owner_id, @kind, @content_type, @size, @now::timestamp, @expires_at::timestamp, @now::timestamp,
        sqlc.narg(duration_ms), sqlc.narg(width), sqlc.narg(height), sqlc.narg(file_name));

-- name: MediaUploadedSince :one
-- bytes a user started uploading since @since (daily quota)
SELECT COALESCE(sum(size), 0)::bigint FROM media WHERE owner_id = @owner_id AND created_at >= @since::timestamp;

-- name: MediaStoredBytes :one
-- bytes currently in R2 (or about to be): the global cap
SELECT COALESCE(sum(size), 0)::bigint FROM media WHERE deleted_at IS NULL;

-- name: GetMedia :one
SELECT * FROM media WHERE id = $1;

-- name: AttachMedia :execrows
-- only the owner's own pending upload, once
UPDATE media SET status = 'ready', chat_type = @chat_type, chat_id = @chat_id, message_id = @message_id
WHERE id = @id AND owner_id = @owner_id AND status = 'pending' AND deleted_at IS NULL;

-- name: MediaForMessages :many
SELECT id, message_id, kind, content_type, size, expires_at, deleted_at, duration_ms, width, height, has_thumbnail, file_name FROM media
WHERE message_id = ANY(@ids::uuid[]) ORDER BY created_at, id;

-- name: RecordMediaDownload :exec
WITH d AS (
    INSERT INTO media_downloads (media_id, user_id) VALUES (@media_id, @user_id) ON CONFLICT DO NOTHING
)
UPDATE media SET last_access_at = @now::timestamp WHERE id = @media_id;

-- name: CountMediaDownloads :one
SELECT count(*)::int FROM media_downloads WHERE media_id = $1;

-- name: MarkMediaDelivered :exec
UPDATE media SET delivered_at = @now::timestamp WHERE id = @id AND delivered_at IS NULL;

-- name: ExpireMediaOfMessage :exec
-- deleting a message deletes its media at the next cleanup
UPDATE media SET expires_at = @now::timestamp WHERE message_id = @message_id AND deleted_at IS NULL;

-- name: ExpireMediaOfGroup :exec
-- a deleted group's media goes at the next cleanup
UPDATE media SET expires_at = @now::timestamp
WHERE chat_type = 'public' AND chat_id = @group_id AND deleted_at IS NULL;

-- name: MediaToDelete :many
-- delivered 24 h ago, past the 7-day limit, or uploads never attached
SELECT id FROM media
WHERE deleted_at IS NULL
  AND (delivered_at < @delivered_before::timestamp
       OR expires_at < @now::timestamp
       OR owner_id IS NULL -- the uploader deleted their account
       OR (status = 'pending' AND created_at < @pending_before::timestamp))
LIMIT 500;

-- name: LeastRecentlyUsedMedia :many
SELECT id, size FROM media WHERE deleted_at IS NULL ORDER BY last_access_at, created_at LIMIT 500;

-- name: MarkMediaDeleted :exec
UPDATE media SET deleted_at = @now::timestamp WHERE id = ANY(@ids::uuid[]);

-- name: SetThumbnail :execrows
-- only the uploader's own, live video
UPDATE media SET has_thumbnail = true
WHERE id = @id AND owner_id = @owner_id AND kind = 'video' AND deleted_at IS NULL AND NOT has_thumbnail;

-- name: ThumbnailsOf :many
SELECT id FROM media WHERE id = ANY(@ids::uuid[]) AND has_thumbnail;

-- name: GroupMediaVisible :one
-- a member sees a group's media if its message was sent since they joined
SELECT EXISTS (
  SELECT 1 FROM member_table mt
  LEFT JOIN GroupMessage gm ON gm.id = sqlc.narg(message_id)::uuid
  WHERE mt.group_id = @group_id AND mt.member_id = @viewer
    AND COALESCE(gm.created_at, @uploaded_at::timestamp) >= mt.joined_at
)::bool;
