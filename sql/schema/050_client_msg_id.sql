-- +goose Up
-- the client_msg_id a message was sent with, returned only to its sender so
-- the app can match a message still waiting on the phone to one that was
-- saved (also after the 7-day sent_ids window). No index: never searched.
ALTER TABLE message ADD COLUMN client_msg_id UUID;
ALTER TABLE GroupMessage ADD COLUMN client_msg_id UUID;

-- +goose Down
ALTER TABLE GroupMessage DROP COLUMN client_msg_id;
ALTER TABLE message DROP COLUMN client_msg_id;
