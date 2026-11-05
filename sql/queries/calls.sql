-- name: InsertCall :exec
INSERT INTO calls (id, caller_id, callee_id, state, started_at, ended_at, end_reason)
VALUES (@id, @caller_id, @callee_id, @state, @started_at::timestamp, sqlc.narg(ended_at)::timestamp, sqlc.narg(end_reason));

-- name: AnswerCall :exec
UPDATE calls SET state = 'active', answered_at = @at::timestamp WHERE id = @id AND state = 'ringing';

-- name: FinishCall :exec
UPDATE calls SET state = @state, end_reason = @end_reason, ended_at = @at::timestamp
WHERE id = @id AND ended_at IS NULL;

-- name: InterruptOpenCalls :execrows
-- calls left open by a restart can't be resumed: the state was in memory
UPDATE calls SET state = CASE WHEN answered_at IS NULL THEN 'failed' ELSE 'ended' END,
                 end_reason = 'interrupted', ended_at = @at::timestamp
WHERE ended_at IS NULL;

-- name: ListCalls :many
-- the caller's call log, newest first; peer is the other person
SELECT c.id, c.caller_id, c.callee_id, c.state, c.end_reason, c.started_at, c.answered_at, c.ended_at,
       u.id AS peer_id, u.name AS peer_name, u.display_name AS peer_display_name, u.avatar_at AS peer_avatar_at
FROM calls c
LEFT JOIN users u ON u.id = CASE WHEN c.caller_id = @me::uuid THEN c.callee_id ELSE c.caller_id END
WHERE (c.caller_id = @me::uuid OR c.callee_id = @me::uuid)
  AND (sqlc.narg(before_at)::timestamp IS NULL OR (c.started_at, c.id) < (sqlc.narg(before_at)::timestamp, @before_id::uuid))
ORDER BY c.started_at DESC, c.id DESC
LIMIT @lim;

-- name: CallStartedAt :one
SELECT started_at FROM calls WHERE id = @id AND (caller_id = @me::uuid OR callee_id = @me::uuid);

-- name: PurgeOldCalls :execrows
DELETE FROM calls WHERE started_at < @before::timestamp;

-- name: CallSecondsSince :one
-- answered call time of a user since @since (calls in progress count up to now)
SELECT COALESCE(SUM(EXTRACT(EPOCH FROM (COALESCE(ended_at, @now::timestamp) - GREATEST(answered_at, @since::timestamp)))), 0)::bigint
FROM calls
WHERE (caller_id = @user_id::uuid OR callee_id = @user_id::uuid)
  AND answered_at IS NOT NULL
  AND COALESCE(ended_at, @now::timestamp) > @since::timestamp;
