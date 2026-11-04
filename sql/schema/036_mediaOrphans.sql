-- +goose Up
-- When an account is deleted its media rows must survive long enough for
-- the cleanup to delete the files from R2 (a cascade would lose track of
-- them). owner_id becomes NULL and the cleanup removes those files.
ALTER TABLE media ALTER COLUMN owner_id DROP NOT NULL;
ALTER TABLE media DROP CONSTRAINT media_owner_id_fkey;
ALTER TABLE media ADD CONSTRAINT media_owner_id_fkey FOREIGN KEY (owner_id) REFERENCES users(id) ON DELETE SET NULL;

-- +goose Down
DELETE FROM media WHERE owner_id IS NULL;
ALTER TABLE media DROP CONSTRAINT media_owner_id_fkey;
ALTER TABLE media ADD CONSTRAINT media_owner_id_fkey FOREIGN KEY (owner_id) REFERENCES users(id) ON DELETE CASCADE;
ALTER TABLE media ALTER COLUMN owner_id SET NOT NULL;
