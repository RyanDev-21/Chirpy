-- +goose Up
-- UI layout settings from the old terminal client; the Android app keeps
-- its own settings on the device.
DROP TABLE IF EXISTS EleConfig;

-- +goose Down
CREATE TABLE EleConfig(
    user_id UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    pref JSONB NOT NULL DEFAULT '[]'
);
