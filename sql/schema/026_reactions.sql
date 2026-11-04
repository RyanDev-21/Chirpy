-- +goose Up
-- One reaction per person per message. Private and group messages live in
-- separate tables, so reactions do too (each with a real foreign key that
-- cascades when the message or the user is deleted).
CREATE TABLE message_reactions (
    message_id UUID NOT NULL REFERENCES message(id) ON DELETE CASCADE,
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    emoji      TEXT NOT NULL CHECK (octet_length(emoji) BETWEEN 1 AND 64),
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    PRIMARY KEY (message_id, user_id)
);
CREATE TABLE group_message_reactions (
    message_id UUID NOT NULL REFERENCES GroupMessage(id) ON DELETE CASCADE,
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    emoji      TEXT NOT NULL CHECK (octet_length(emoji) BETWEEN 1 AND 64),
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    PRIMARY KEY (message_id, user_id)
);

-- +goose Down
DROP TABLE group_message_reactions;
DROP TABLE message_reactions;
