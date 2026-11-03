-- +goose Up
-- One relationship row per pair of users regardless of direction, so
-- postgres rejects duplicate and crossing friend requests. Keep the
-- confirmed row if there is one, otherwise the oldest.
DELETE FROM user_relationships
WHERE id IN (
    SELECT id FROM (
        SELECT id, ROW_NUMBER() OVER (
            PARTITION BY LEAST(user_id, otherUser_id), GREATEST(user_id, otherUser_id)
            ORDER BY (status = 'confirm') DESC, created_at, id
        ) AS rn
        FROM user_relationships
    ) ranked
    WHERE rn > 1
);
CREATE UNIQUE INDEX user_relationships_pair_key
    ON user_relationships (LEAST(user_id, otherUser_id), GREATEST(user_id, otherUser_id));
ALTER TABLE user_relationships ADD CONSTRAINT user_relationships_not_self CHECK (user_id <> otherUser_id);

-- +goose Down
ALTER TABLE user_relationships DROP CONSTRAINT user_relationships_not_self;
DROP INDEX user_relationships_pair_key;
