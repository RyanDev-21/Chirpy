-- +goose Up
-- Videos carry what the phone measured (so the chat shows "0:42" and the
-- right shape before downloading) and may have a small still thumbnail,
-- stored in R2 next to the video (key t/{id}) and deleted with it.
ALTER TABLE media ADD COLUMN duration_ms INT CHECK (duration_ms BETWEEN 1 AND 600000);
ALTER TABLE media ADD COLUMN width INT CHECK (width BETWEEN 1 AND 8192);
ALTER TABLE media ADD COLUMN height INT CHECK (height BETWEEN 1 AND 8192);
ALTER TABLE media ADD COLUMN has_thumbnail BOOLEAN NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE media DROP COLUMN has_thumbnail;
ALTER TABLE media DROP COLUMN height;
ALTER TABLE media DROP COLUMN width;
ALTER TABLE media DROP COLUMN duration_ms;
