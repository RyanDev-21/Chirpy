-- name: DiscoverSuggestions :many
-- Opt-in users who aren't already connected with @me (no friendship,
-- request or block either way), ranked by mutual friends, shared
-- interests and place. Only people with at least one of those appear.
WITH me AS (
    SELECT location_id, location_country FROM users WHERE id = @me::uuid
), connected AS (
    SELECT CASE WHEN user_id = @me::uuid THEN otherUser_id ELSE user_id END AS uid
    FROM user_relationships WHERE user_id = @me::uuid OR otherUser_id = @me::uuid
), my_friends AS (
    SELECT CASE WHEN user_id = @me::uuid THEN otherUser_id ELSE user_id END AS fid
    FROM user_relationships
    WHERE status = 'confirm' AND (user_id = @me::uuid OR otherUser_id = @me::uuid)
), mutual AS (
    SELECT CASE WHEN r.user_id = f.fid THEN r.otherUser_id ELSE r.user_id END AS uid, count(*) AS n
    FROM my_friends f
    JOIN user_relationships r ON r.status = 'confirm' AND (r.user_id = f.fid OR r.otherUser_id = f.fid)
    GROUP BY 1
), shared AS (
    SELECT o.user_id AS uid, array_agg(o.interest_id ORDER BY o.interest_id) AS ids
    FROM user_interests mine
    JOIN user_interests o ON o.interest_id = mine.interest_id AND o.user_id <> @me::uuid
    WHERE mine.user_id = @me::uuid
    GROUP BY o.user_id
), scored AS (
    SELECT u.id, u.name, u.avatar_at, u.location_id,
           COALESCE(m.n, 0)::int AS mutual_friends,
           COALESCE(s.ids, '{}'::text[]) AS shared_interests,
           (u.location_id IS NOT NULL AND u.location_id = me.location_id) AS same_place,
           (u.location_country <> '' AND u.location_country = me.location_country) AS same_country
    FROM users u
    CROSS JOIN me
    LEFT JOIN mutual m ON m.uid = u.id
    LEFT JOIN shared s ON s.uid = u.id
    WHERE u.discoverable
      AND u.banned_at IS NULL
      AND u.id <> @me::uuid
      AND u.id NOT IN (SELECT uid FROM connected)
      AND NOT EXISTS (
          SELECT 1 FROM user_blocks b
          WHERE (b.blocker_id = u.id AND b.blocked_id = @me::uuid)
             OR (b.blocker_id = @me::uuid AND b.blocked_id = u.id)
      )
)
SELECT id, name, avatar_at, location_id, mutual_friends, shared_interests::text[] AS shared_interests,
       same_place::bool AS same_place,
       (3 * mutual_friends + 2 * cardinality(shared_interests)
        + CASE WHEN same_place THEN 3 WHEN same_country THEN 1 ELSE 0 END)::int AS score
FROM scored
WHERE mutual_friends > 0 OR cardinality(shared_interests) > 0 OR same_place OR same_country
ORDER BY score DESC, id
LIMIT @lim OFFSET @off;
