-- +goose Up
-- friends mentioned in a story (a mention sticker); only they may repost it
CREATE TABLE story_mentions (
    story_id UUID NOT NULL REFERENCES stories(id) ON DELETE CASCADE,
    user_id  UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    PRIMARY KEY (story_id, user_id)
);

-- a repost of a story someone was mentioned in: it goes when the original
-- goes (deleted early, or expired)
ALTER TABLE stories ADD COLUMN repost_of UUID REFERENCES stories(id) ON DELETE CASCADE;
CREATE UNIQUE INDEX stories_one_repost ON stories (repost_of, user_id) WHERE repost_of IS NOT NULL;

-- +goose Down
DROP INDEX stories_one_repost;
ALTER TABLE stories DROP COLUMN repost_of;
DROP TABLE story_mentions;
