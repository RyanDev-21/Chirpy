-- +goose Up
CREATE TABLE seen_messages (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    chat_id VARCHAR(72) NOT NULL,
    message_id UUID REFERENCES message(id) NOT NULL,
    seen_id UUID REFERENCES users(id)  ON DELETE CASCADE,
    updated_time TIMESTAMP NOT NULL DEFAULT NOW() 
);

-- +goose Down
DROP TABLE seen_messages;
