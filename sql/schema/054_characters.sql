-- +goose Up
-- the pieces of a user's character (validated against the catalog), and a
-- short hash of them that changes when they do (for caching renders)
ALTER TABLE users ADD COLUMN character JSONB;
ALTER TABLE users ADD COLUMN character_v TEXT;

-- an animation played with the sender's character (catalog id)
ALTER TABLE message ADD COLUMN anim TEXT;
ALTER TABLE GroupMessage ADD COLUMN anim TEXT;

-- a reaction can carry an animation of the reactor's character
ALTER TABLE message_reactions ADD COLUMN anim TEXT;
ALTER TABLE group_message_reactions ADD COLUMN anim TEXT;

-- +goose Down
ALTER TABLE group_message_reactions DROP COLUMN anim;
ALTER TABLE message_reactions DROP COLUMN anim;
ALTER TABLE GroupMessage DROP COLUMN anim;
ALTER TABLE message DROP COLUMN anim;
ALTER TABLE users DROP COLUMN character_v;
ALTER TABLE users DROP COLUMN character;
