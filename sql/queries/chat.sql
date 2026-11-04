-- name: AddMessagePrivate :exec
INSERT INTO message(id, content, parentId, from_id, to_id, created_at)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: AddMessagePublic :exec
INSERT INTO GroupMessage(id, content, group_id, from_id, parent_id, created_at)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: GetRecentPrivateMessages :many
SELECT * FROM message
WHERE ((from_id = @user_a AND to_id = @user_b) OR (from_id = @user_b AND to_id = @user_a))
ORDER BY created_at DESC
LIMIT @lim;

-- name: GetPrivateMessagesSince :many
SELECT * FROM message
WHERE ((from_id = @user_a AND to_id = @user_b) OR (from_id = @user_b AND to_id = @user_a))
  AND created_at > @since
ORDER BY created_at ASC
LIMIT @lim;

-- name: GetRecentGroupMessages :many
SELECT * FROM GroupMessage
WHERE group_id = @group_id
ORDER BY created_at DESC
LIMIT @lim;

-- name: AddLastSeenMessage :exec
INSERT INTO seen_messages(chat_id, message_id, seen_id)
VALUES ($1, $2, $3)
ON CONFLICT (chat_id, seen_id)
DO UPDATE SET message_id = EXCLUDED.message_id, updated_time = NOW();

-- name: PrivateMessageInChat :one
SELECT EXISTS (
    SELECT 1 FROM message
    WHERE id = @id
      AND ((from_id = @user_a AND to_id = @user_b) OR (from_id = @user_b AND to_id = @user_a))
);

-- name: GroupMessageInGroup :one
SELECT EXISTS (SELECT 1 FROM GroupMessage WHERE id = @id AND group_id = @group_id);

-- name: GetPrivateMessagesBefore :many
-- the page of messages just older than the cursor message (newest first)
SELECT * FROM message
WHERE ((from_id = @user_a AND to_id = @user_b) OR (from_id = @user_b AND to_id = @user_a))
  AND (created_at, id) < (@before_time::timestamp, @before_id::uuid)
ORDER BY created_at DESC, id DESC
LIMIT @lim;

-- name: GetGroupMessagesBefore :many
SELECT * FROM GroupMessage
WHERE group_id = @group_id
  AND (created_at, id) < (@before_time::timestamp, @before_id::uuid)
ORDER BY created_at DESC, id DESC
LIMIT @lim;

-- name: PrivateMessageCursor :one
-- created_at of a message, only if it belongs to the conversation
SELECT created_at FROM message
WHERE id = @id AND ((from_id = @user_a AND to_id = @user_b) OR (from_id = @user_b AND to_id = @user_a));

-- name: GroupMessageCursor :one
SELECT created_at FROM GroupMessage WHERE id = @id AND group_id = @group_id;

-- name: MarkGroupRead :exec
-- only moves forward: an older message never replaces a newer marker
INSERT INTO group_reads (group_id, user_id, message_id)
VALUES (@group_id, @user_id, @message_id)
ON CONFLICT (group_id, user_id) DO UPDATE SET message_id = EXCLUDED.message_id, updated_at = NOW()
WHERE (SELECT created_at FROM GroupMessage WHERE id = EXCLUDED.message_id)
   >= (SELECT created_at FROM GroupMessage WHERE id = group_reads.message_id);

-- name: ListPrivateConversations :many
-- one row per person the caller has exchanged messages with: the latest
-- message and how many of theirs arrived after the caller's read marker
WITH mine AS (
    SELECT m.id, m.content, m.from_id, m.created_at, m.deleted_at,
           CASE WHEN m.from_id = @me::uuid THEN m.to_id ELSE m.from_id END AS other_id,
           -- same as chat.PrivateChatID: the two ids sorted bytewise, joined by _
           LEAST(m.from_id::text COLLATE "C", m.to_id::text COLLATE "C") || '_' ||
               GREATEST(m.from_id::text COLLATE "C", m.to_id::text COLLATE "C") AS chat_key
    FROM message m
    WHERE (m.from_id = @me::uuid OR m.to_id = @me::uuid)
), latest AS (
    SELECT DISTINCT ON (other_id) * FROM mine
    WHERE other_id IS NOT NULL
    ORDER BY other_id, created_at DESC, id DESC
)
SELECT l.other_id::uuid AS other_id, u.name AS other_name,
       l.id AS last_id, l.content AS last_content, l.from_id AS last_from, l.created_at AS last_at,
       (l.deleted_at IS NOT NULL)::bool AS last_deleted,
       (SELECT count(*) FROM message x
        WHERE x.from_id = l.other_id AND x.to_id = @me::uuid AND x.deleted_at IS NULL
          AND x.created_at > COALESCE((
              SELECT sm.created_at FROM seen_messages s JOIN message sm ON sm.id = s.message_id
              WHERE s.seen_id = @me::uuid AND s.chat_id = l.chat_key
          ), '-infinity'::timestamp)
       )::int AS unread
FROM latest l
JOIN users u ON u.id = l.other_id
ORDER BY l.created_at DESC, l.id DESC;

-- name: ListGroupConversations :many
-- every group the caller is in, with its latest message (if any) and how
-- many messages from others arrived after the caller's read marker
SELECT g.id AS group_id, g.name AS group_name,
       -- zero uuid when the group has no messages yet (last_at is then NULL)
       COALESCE(lm.id, '00000000-0000-0000-0000-000000000000'::uuid)::uuid AS last_id, lm.content AS last_content, lm.from_id AS last_from, lm.created_at AS last_at,
       COALESCE(lm.deleted_at IS NOT NULL, false)::bool AS last_deleted,
       (SELECT count(*) FROM GroupMessage x
        WHERE x.group_id = g.id AND x.deleted_at IS NULL
          AND (x.from_id IS NULL OR x.from_id <> @me::uuid)
          AND x.created_at > COALESCE((
              SELECT rm.created_at FROM group_reads r JOIN GroupMessage rm ON rm.id = r.message_id
              WHERE r.group_id = g.id AND r.user_id = @me::uuid
          ), m.joined_at)
       )::int AS unread
FROM member_table m
JOIN chat_groups g ON g.id = m.group_id
LEFT JOIN LATERAL (
    SELECT gm.id, gm.content, gm.from_id, gm.created_at, gm.deleted_at FROM GroupMessage gm
    WHERE gm.group_id = g.id
    ORDER BY gm.created_at DESC, gm.id DESC LIMIT 1
) lm ON true
WHERE m.member_id = @me::uuid
ORDER BY lm.created_at DESC NULLS LAST, lower(g.name);

-- name: PrivateMessageChanges :many
-- edit/delete state for a page of messages (overlaid on cached copies)
SELECT id, content, edited_at, deleted_at FROM message
WHERE id = ANY(@ids::uuid[]) AND (edited_at IS NOT NULL OR deleted_at IS NOT NULL);

-- name: GroupMessageChanges :many
SELECT id, content, edited_at, deleted_at FROM GroupMessage
WHERE id = ANY(@ids::uuid[]) AND (edited_at IS NOT NULL OR deleted_at IS NOT NULL);

-- name: EditPrivateMessage :execrows
UPDATE message SET content = @content, edited_at = @at::timestamp, updated_at = @at::timestamp
WHERE id = @id AND deleted_at IS NULL;

-- name: EditGroupMessage :execrows
UPDATE GroupMessage SET content = @content, edited_at = @at::timestamp, updated_at = @at::timestamp
WHERE id = @id AND deleted_at IS NULL;

-- name: DeletePrivateMessage :one
-- wipes the content, keeps the row as a placeholder, drops its reactions
WITH d AS (
    UPDATE message SET content = NULL, deleted_at = @at::timestamp, updated_at = @at::timestamp
    WHERE id = @id AND deleted_at IS NULL
    RETURNING id
), r AS (
    DELETE FROM message_reactions WHERE message_id IN (SELECT id FROM d)
)
SELECT count(*)::int FROM d;

-- name: DeleteGroupMessage :one
WITH d AS (
    UPDATE GroupMessage SET content = NULL, deleted_at = @at::timestamp, updated_at = @at::timestamp
    WHERE id = @id AND deleted_at IS NULL
    RETURNING id
), r AS (
    DELETE FROM group_message_reactions WHERE message_id IN (SELECT id FROM d)
)
SELECT count(*)::int FROM d;
