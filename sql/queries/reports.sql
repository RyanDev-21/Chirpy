-- name: UpsertOpenReport :one
-- one open report per pair: reporting again updates it
INSERT INTO reports (id, reporter_id, reported_id, reason, note, created_at)
VALUES (@id, @reporter_id, @reported_id, @reason, @note, @now::timestamp)
ON CONFLICT (reporter_id, reported_id) WHERE status = 'open'
DO UPDATE SET reason = EXCLUDED.reason, note = EXCLUDED.note, created_at = EXCLUDED.created_at
RETURNING id;

-- name: PrivateEvidence :many
-- messages of the reporter's private chat with the reported user
SELECT id, content, from_id, created_at FROM message
WHERE id = ANY(@ids::uuid[]) AND deleted_at IS NULL
  AND ((from_id = @reporter::uuid AND to_id = @reported::uuid) OR (from_id = @reported::uuid AND to_id = @reporter::uuid));

-- name: GroupEvidence :many
-- messages of groups the reporter is a member of
SELECT gm.id, gm.content, gm.from_id, gm.group_id, gm.created_at FROM GroupMessage gm
JOIN member_table m ON m.group_id = gm.group_id AND m.member_id = @reporter::uuid
WHERE gm.id = ANY(@ids::uuid[]) AND gm.deleted_at IS NULL;

-- name: AddReportMessage :exec
INSERT INTO report_messages (report_id, message_id, chat_type, group_id, sender_id, content, sent_at)
VALUES (@report_id, @message_id, @chat_type, sqlc.narg(group_id), sqlc.narg(sender_id), @content, @sent_at::timestamp)
ON CONFLICT DO NOTHING;

-- name: ListReports :many
SELECT r.id, r.reason, r.status, r.created_at, r.reported_id,
       COALESCE(ru.name, '') AS reported_name, COALESCE(pu.name, '(deleted)') AS reporter_name,
       (SELECT count(*) FROM report_messages m WHERE m.report_id = r.id)::int AS evidence,
       (SELECT count(*) FROM reports o WHERE o.reported_id = r.reported_id)::int AS reports_against
FROM reports r
LEFT JOIN users ru ON ru.id = r.reported_id
LEFT JOIN users pu ON pu.id = r.reporter_id
WHERE @status::text = '' OR r.status = @status::text
ORDER BY r.created_at DESC
LIMIT @lim OFFSET @off;

-- name: GetReport :one
SELECT r.*, COALESCE(ru.name, '') AS reported_name, COALESCE(pu.name, '(deleted)') AS reporter_name
FROM reports r
LEFT JOIN users ru ON ru.id = r.reported_id
LEFT JOIN users pu ON pu.id = r.reporter_id
WHERE r.id = $1;

-- name: ReportMessages :many
SELECT message_id, chat_type, group_id, sender_id, content, sent_at FROM report_messages
WHERE report_id = $1 ORDER BY sent_at;

-- name: ResolveReport :execrows
UPDATE reports SET status = @status, resolved_by = @admin_id, resolved_at = @now::timestamp, resolution = @resolution
WHERE id = @id AND status = 'open';

-- name: CountOpenReports :one
SELECT count(*)::bigint FROM reports WHERE status = 'open';

-- name: DeleteOldClosedReports :execrows
-- closed reports (and their copied evidence) are kept 180 days
DELETE FROM reports WHERE status <> 'open' AND resolved_at < @before::timestamp;
