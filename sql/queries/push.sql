-- name: UpsertDevice :exec
INSERT INTO user_devices (token, user_id, platform) VALUES (@token, @user_id, @platform)
ON CONFLICT (token) DO UPDATE SET user_id = EXCLUDED.user_id, platform = EXCLUDED.platform, last_seen_at = NOW();

-- name: DeleteDevice :exec
DELETE FROM user_devices WHERE token = @token AND user_id = @user_id;

-- name: DeleteUserDevices :exec
DELETE FROM user_devices WHERE user_id = $1;

-- name: DeleteDeadTokens :exec
DELETE FROM user_devices WHERE token = ANY(@tokens::text[]);

-- name: PushTargets :many
-- devices of the given users who want this kind of notification and
-- haven't muted this chat
SELECT d.user_id, d.token FROM user_devices d
LEFT JOIN notification_prefs p ON p.user_id = d.user_id
WHERE d.user_id = ANY(@users::uuid[])
  AND CASE @kind::text
        WHEN 'private' THEN COALESCE(p.private, true)
        WHEN 'public' THEN COALESCE(p.groups, true)
        WHEN 'friends' THEN COALESCE(p.friends, true)
        WHEN 'reactions' THEN COALESCE(p.reactions, true)
        WHEN 'calls' THEN true -- calls always ring (mute the person to stop it)
        ELSE false END
  AND NOT EXISTS (
      SELECT 1 FROM chat_mutes m
      WHERE m.user_id = d.user_id AND m.chat_type = @chat_type::text AND m.chat_id = @chat_id::uuid
        AND (m.until IS NULL OR m.until > @now::timestamp)
  );

-- name: GetNotificationPrefs :one
SELECT private, groups, friends, reactions FROM notification_prefs WHERE user_id = $1;

-- name: SetNotificationPrefs :exec
INSERT INTO notification_prefs (user_id, private, groups, friends, reactions)
VALUES (@user_id, @private, @groups, @friends, @reactions)
ON CONFLICT (user_id) DO UPDATE SET private = EXCLUDED.private, groups = EXCLUDED.groups,
    friends = EXCLUDED.friends, reactions = EXCLUDED.reactions;

-- name: SetMute :exec
INSERT INTO chat_mutes (user_id, chat_type, chat_id, until) VALUES (@user_id, @chat_type, @chat_id, sqlc.narg(until))
ON CONFLICT (user_id, chat_type, chat_id) DO UPDATE SET until = EXCLUDED.until;

-- name: DeleteMute :exec
DELETE FROM chat_mutes WHERE user_id = @user_id AND chat_type = @chat_type AND chat_id = @chat_id;

-- name: ListMutes :many
SELECT chat_type, chat_id, until FROM chat_mutes
WHERE user_id = @user_id AND (until IS NULL OR until > @now::timestamp)
ORDER BY chat_type, chat_id;
