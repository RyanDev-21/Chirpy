-- +goose Up
-- denied requests used to be kept with status 'cancel'; they blocked new
-- requests through UNIQUE(user_id, otherUser_id) and reappeared as pending
DELETE FROM user_relationships WHERE status = 'cancel';
CREATE INDEX IF NOT EXISTS idx_user_rel_other ON user_relationships(otherUser_id, status);

-- +goose Down
DROP INDEX IF EXISTS idx_user_rel_other;
