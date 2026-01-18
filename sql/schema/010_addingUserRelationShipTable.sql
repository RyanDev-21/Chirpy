-- +goose Up
CREATE TABLE user_relationships(
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    otherUser_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,    
    label VARCHAR(20) NOT NULL DEFAULT 'friend',
    UNIQUE(user_id,otherUser_id),
    status VARCHAR(20) NOT NULL DEFAULT 'pending',
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW()
);

ALTER TABLE message ALTER COLUMN id SET DEFAULT gen_random_uuid();
ALTER TABLE message ALTER COLUMN parentId SET DEFAULT NULL;
-- +goose Down
DROP TABLE user_relationships;
ALTER TABLE message ALTER COLUMN id DROP DEFAULT;
ALTER TABLE message ALTER COLUMN parentId DROP DEFAULT;
