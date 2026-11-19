-- +goose Up
-- Stories: a photo shown to the poster's friends for 24 hours. The photo is
-- a media row (chat_type 'story', message_id = the story) that expires
-- with the story, so the usual media cleanup removes it from R2.
CREATE TABLE stories (
    id         UUID PRIMARY KEY,
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMP NOT NULL,
    expires_at TIMESTAMP NOT NULL
);
CREATE INDEX idx_stories_user ON stories (user_id, expires_at);
CREATE INDEX idx_stories_expires ON stories (expires_at);

-- who saw which story (only the poster sees the list)
CREATE TABLE story_views (
    story_id  UUID NOT NULL REFERENCES stories(id) ON DELETE CASCADE,
    viewer_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    viewed_at TIMESTAMP NOT NULL,
    PRIMARY KEY (story_id, viewer_id)
);

-- whose stories I don't want to see
CREATE TABLE story_mutes (
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    muted_id   UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMP NOT NULL,
    PRIMARY KEY (user_id, muted_id)
);

ALTER TABLE media DROP CONSTRAINT media_chat_type_check;
ALTER TABLE media ADD CONSTRAINT media_chat_type_check CHECK (chat_type IN ('private', 'public', 'story'));

-- a private message replying to a story (no foreign key: the story is
-- deleted after 24 h, the reply stays)
ALTER TABLE message ADD COLUMN story_id UUID;

-- +goose Down
ALTER TABLE message DROP COLUMN story_id;
DELETE FROM media WHERE chat_type = 'story';
ALTER TABLE media DROP CONSTRAINT media_chat_type_check;
ALTER TABLE media ADD CONSTRAINT media_chat_type_check CHECK (chat_type IN ('private', 'public'));
DROP TABLE story_mutes;
DROP TABLE story_views;
DROP TABLE stories;
