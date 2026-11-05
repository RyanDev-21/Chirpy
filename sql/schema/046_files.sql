-- +goose Up
-- files (documents, archives, audio) next to photos and videos; the name
-- the sender gave it is shown to recipients and used when saving
ALTER TABLE media DROP CONSTRAINT media_kind_check;
ALTER TABLE media ADD CONSTRAINT media_kind_check CHECK (kind IN ('image', 'video', 'file'));
ALTER TABLE media ADD COLUMN file_name TEXT CHECK (char_length(file_name) <= 200);

-- +goose Down
DELETE FROM media WHERE kind = 'file';
ALTER TABLE media DROP COLUMN file_name;
ALTER TABLE media DROP CONSTRAINT media_kind_check;
ALTER TABLE media ADD CONSTRAINT media_kind_check CHECK (kind IN ('image', 'video'));
