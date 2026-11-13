-- name: AddMessagePrivate :exec
INSERT INTO message(id, content, parentId, from_id, to_id, created_at, forwarded)
VALUES ($1, $2, $3, $4, $5, $6, @forwarded);

-- name: AddMessagePublic :exec
-- searchable: groups of up to 50 members are indexed for search
INSERT INTO GroupMessage(id, content, group_id, from_id, parent_id, created_at, searchable, forwarded)
VALUES ($1, $2, $3, $4, $5, $6, COALESCE((SELECT c.current_member <= 50 FROM chat_groups c WHERE c.id = $3), false), @forwarded);

-- name: AddMessagePrivateOnce :execrows
-- saves the message unless @from_id already used @client_id (0 rows then)
WITH s AS (
    INSERT INTO sent_ids (from_id, client_id, msg_id, created_at)
    VALUES (@from_id, @client_id, @id, @created_at)
    ON CONFLICT DO NOTHING
    RETURNING msg_id
)
INSERT INTO message(id, content, parentId, from_id, to_id, created_at, forwarded, client_msg_id)
SELECT @id, @content, @parentid, @from_id, @to_id, @created_at, @forwarded, @client_id FROM s;

-- name: AddMessagePublicOnce :execrows
WITH s AS (
    INSERT INTO sent_ids (from_id, client_id, msg_id, created_at)
    VALUES (@from_id, @client_id, @id, @created_at)
    ON CONFLICT DO NOTHING
    RETURNING msg_id
)
INSERT INTO GroupMessage(id, content, group_id, from_id, parent_id, created_at, searchable, forwarded, client_msg_id)
SELECT @id, @content, @group_id, @from_id, @parent_id, @created_at,
       COALESCE((SELECT c.current_member <= 50 FROM chat_groups c WHERE c.id = @group_id), false), @forwarded, @client_id FROM s;

-- name: SentMessageID :one
SELECT msg_id FROM sent_ids WHERE from_id = @from_id AND client_id = @client_id;

-- name: DeleteOldSentIDs :execrows
DELETE FROM sent_ids WHERE created_at < @before::timestamp;

-- name: PrivateChangesSince :many
-- messages in @me's private chats edited, deleted or reacted to after @since
SELECT * FROM message
WHERE changed_at > @since::timestamp AND (from_id = @me::uuid OR to_id = @me::uuid)
ORDER BY changed_at ASC, id ASC
LIMIT @lim;

-- name: GroupChangesSince :many
-- the same for @me's groups, only messages sent since they joined
SELECT g.* FROM GroupMessage g
JOIN member_table mt ON mt.group_id = g.group_id AND mt.member_id = @me::uuid
WHERE g.changed_at > @since::timestamp AND g.created_at >= mt.joined_at
ORDER BY g.changed_at ASC, g.id ASC
LIMIT @lim;

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
-- only what was sent since the viewer joined
SELECT g.* FROM GroupMessage g
WHERE g.group_id = @group_id
  AND g.created_at >= (SELECT mt.joined_at FROM member_table mt WHERE mt.group_id = @group_id AND mt.member_id = @viewer::uuid)
ORDER BY g.created_at DESC
LIMIT @lim;

-- name: AddLastSeenMessage :exec
-- only moves forward: a late or repeated receipt never moves it back
INSERT INTO seen_messages(chat_id, message_id, seen_id)
VALUES ($1, $2, $3)
ON CONFLICT (chat_id, seen_id)
DO UPDATE SET message_id = EXCLUDED.message_id
WHERE (SELECT created_at FROM message WHERE id = EXCLUDED.message_id)
   >= (SELECT created_at FROM message WHERE id = seen_messages.message_id);

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
SELECT g.* FROM GroupMessage g
WHERE g.group_id = @group_id
  AND (g.created_at, g.id) < (@before_time::timestamp, @before_id::uuid)
  AND g.created_at >= (SELECT mt.joined_at FROM member_table mt WHERE mt.group_id = @group_id AND mt.member_id = @viewer::uuid)
ORDER BY g.created_at DESC, g.id DESC
LIMIT @lim;

-- name: PrivateMessageCursor :one
-- created_at of a message, only if it belongs to the conversation
SELECT created_at FROM message
WHERE id = @id AND ((from_id = @user_a AND to_id = @user_b) OR (from_id = @user_b AND to_id = @user_a));

-- name: GroupMessageCursor :one
SELECT created_at FROM GroupMessage WHERE id = @id AND group_id = @group_id;

-- name: MarkGroupRead :execrows
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
SELECT l.other_id::uuid AS other_id, u.name AS other_name, u.display_name AS other_display, u.avatar_at AS other_avatar_at,
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
SELECT g.id AS group_id, g.name AS group_name, g.avatar_at AS group_avatar_at,
       -- zero uuid when the group has no messages yet (last_at is then NULL)
       COALESCE(lm.id, '00000000-0000-0000-0000-000000000000'::uuid)::uuid AS last_id, lm.content AS last_content, lm.from_id AS last_from, lm.created_at AS last_at,
       COALESCE(lm.deleted_at IS NOT NULL, false)::bool AS last_deleted,
       COALESCE(su.name, '')::text AS last_from_name, COALESCE(su.display_name, '')::text AS last_from_display,
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
    WHERE gm.group_id = g.id AND gm.created_at >= m.joined_at
    ORDER BY gm.created_at DESC, gm.id DESC LIMIT 1
) lm ON true
LEFT JOIN users su ON su.id = lm.from_id
WHERE m.member_id = @me::uuid
ORDER BY lm.created_at DESC NULLS LAST, lower(g.name);

-- name: EditPrivateMessage :execrows
UPDATE message SET content = @content, edited_at = @at::timestamp, changed_at = @at::timestamp
WHERE id = @id AND deleted_at IS NULL;

-- name: EditGroupMessage :execrows
UPDATE GroupMessage SET content = @content, edited_at = @at::timestamp, changed_at = @at::timestamp
WHERE id = @id AND deleted_at IS NULL;

-- name: DeletePrivateMessage :one
-- wipes the content, keeps the row as a placeholder, drops its reactions
WITH d AS (
    UPDATE message SET content = NULL, deleted_at = @at::timestamp, changed_at = @at::timestamp
    WHERE id = @id AND deleted_at IS NULL
    RETURNING id
), r AS (
    DELETE FROM message_reactions WHERE message_id IN (SELECT id FROM d)
)
SELECT count(*)::int FROM d;

-- name: DeleteGroupMessage :one
WITH d AS (
    UPDATE GroupMessage SET content = NULL, deleted_at = @at::timestamp, changed_at = @at::timestamp
    WHERE id = @id AND deleted_at IS NULL
    RETURNING id
), r AS (
    DELETE FROM group_message_reactions WHERE message_id IN (SELECT id FROM d)
)
SELECT count(*)::int FROM d;

-- name: GroupReadBy :many
-- members (not the sender) whose read marker is at or past the message
SELECT u.id, u.name, u.display_name, u.avatar_at, r.updated_at AS read_at
FROM group_reads r
JOIN GroupMessage rm ON rm.id = r.message_id
JOIN users u ON u.id = r.user_id
JOIN member_table m ON m.group_id = r.group_id AND m.member_id = r.user_id
WHERE r.group_id = @group_id AND r.user_id <> @sender::uuid
  AND (rm.created_at, rm.id) >= (@msg_at::timestamp, @msg_id::uuid)
ORDER BY r.updated_at;

-- name: RecentGroupSenders :many
-- who sent the last 50 messages up to and including msg_id: the people a
-- read marker at msg_id gives "read" ticks to
SELECT DISTINCT t.from_id::uuid FROM (
  SELECT g.from_id FROM GroupMessage g
  WHERE g.group_id = @group_id
    AND (g.created_at, g.id) <= (SELECT m.created_at, m.id FROM GroupMessage m WHERE m.id = @msg_id::uuid)
  ORDER BY g.created_at DESC, g.id DESC
  LIMIT 50
) t WHERE t.from_id IS NOT NULL;

-- name: GroupJoinedAt :one
SELECT joined_at FROM member_table WHERE group_id = @group_id AND member_id = @member_id;

-- name: SearchPrivateMessages :many
-- my private messages containing q (lower-cased, LIKE-escaped), newest
-- first, optionally with one person, older than the cursor
SELECT m.id, m.content, m.from_id, m.to_id, m.created_at FROM message m
WHERE (m.from_id = @me::uuid OR m.to_id = @me::uuid)
  AND m.deleted_at IS NULL
  AND lower(m.content) LIKE '%' || @q::text || '%' ESCAPE '\'
  AND (sqlc.narg(with_user)::uuid IS NULL
       OR (m.from_id = @me::uuid AND m.to_id = sqlc.narg(with_user)::uuid)
       OR (m.to_id = @me::uuid AND m.from_id = sqlc.narg(with_user)::uuid))
  AND (m.created_at, m.id) < (@before_at::timestamp, @before_id::uuid)
  AND (sqlc.narg(after_at)::timestamp IS NULL OR m.created_at >= sqlc.narg(after_at)::timestamp)
ORDER BY m.created_at DESC, m.id DESC
LIMIT @lim;

-- name: SearchGroupMessages :many
-- searchable messages of my groups containing q, sent since I joined
SELECT g.id, g.content, g.from_id, g.group_id, g.created_at FROM GroupMessage g
JOIN member_table mt ON mt.group_id = g.group_id AND mt.member_id = @me::uuid
WHERE g.searchable AND g.deleted_at IS NULL
  AND lower(g.content) LIKE '%' || @q::text || '%' ESCAPE '\'
  AND g.created_at >= mt.joined_at
  AND (sqlc.narg(in_group)::uuid IS NULL OR g.group_id = sqlc.narg(in_group)::uuid)
  AND (g.created_at, g.id) < (@before_at::timestamp, @before_id::uuid)
  AND (sqlc.narg(after_at)::timestamp IS NULL OR g.created_at >= sqlc.narg(after_at)::timestamp)
ORDER BY g.created_at DESC, g.id DESC
LIMIT @lim;

-- name: PrivateMessageContent :one
SELECT content, deleted_at FROM message WHERE id = $1;

-- name: GroupMessageContent :one
SELECT content, deleted_at FROM GroupMessage WHERE id = $1;

-- name: PeerSeenMessage :one
-- how far the other person has read a private chat (their read marker)
SELECT message_id FROM seen_messages WHERE chat_id = @chat_id AND seen_id = @seen_id;

-- name: GetGroupMessagesSince :many
-- catch-up after a reconnect: newer than @since, oldest first, only what
-- the viewer can see (sent since they joined)
SELECT g.* FROM GroupMessage g
WHERE g.group_id = @group_id
  AND g.created_at > @since::timestamp
  AND g.created_at >= (SELECT mt.joined_at FROM member_table mt WHERE mt.group_id = @group_id AND mt.member_id = @viewer::uuid)
ORDER BY g.created_at ASC, g.id ASC
LIMIT @lim;
