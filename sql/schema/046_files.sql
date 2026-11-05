-- +goose Up
-- files (documents, archives, audio) and voice messages next to photos and
-- videos. A file's name is shown to recipients and used when saving; a voice
-- message's waveform (bar heights 0-255) is shown before downloading.
ALTER TABLE media DROP CONSTRAINT media_kind_check;
ALTER TABLE media ADD CONSTRAINT media_kind_check CHECK (kind IN ('image', 'video', 'file', 'voice'));
ALTER TABLE media ADD COLUMN file_name TEXT CHECK (char_length(file_name) <= 200);
ALTER TABLE media ADD COLUMN waveform BYTEA CHECK (octet_length(waveform) <= 128);

-- +goose Down
DELETE FROM media WHERE kind IN ('file', 'voice');
ALTER TABLE media DROP COLUMN waveform;
ALTER TABLE media DROP COLUMN file_name;
ALTER TABLE media DROP CONSTRAINT media_kind_check;
ALTER TABLE media ADD CONSTRAINT media_kind_check CHECK (kind IN ('image', 'video'));
