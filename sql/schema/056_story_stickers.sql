-- +goose Up
-- stickers played over a story's photo (pigeons doing an animation),
-- validated by the server; NULL when there are none
ALTER TABLE stories ADD COLUMN stickers JSONB;

-- +goose Down
ALTER TABLE stories DROP COLUMN stickers;
