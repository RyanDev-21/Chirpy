-- +goose Up
-- when recipients who hadn't downloaded a big file were reminded that it's
-- about to be deleted (once per file)
ALTER TABLE media ADD COLUMN reminded_at TIMESTAMP;

-- +goose Down
ALTER TABLE media DROP COLUMN reminded_at;
