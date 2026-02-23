-- +goose Up
CREATE TABLE EleConfig(
	user_id UUID PRIMARY KEY  REFERENCES users(id) ON DELETE CASCADE ,
	pref jsonb NOT NULL DEFAULT '{}'
);

-- +goose Down
DROP TABLE EleConfig;

