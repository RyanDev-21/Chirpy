-- +goose Up
-- location_id is an id from the app's place list (a city such as
-- "th-bangkok", a country such as "mm", or a region such as "europe"); no
-- coordinates are stored. Discovery is opt-in.
ALTER TABLE users ADD COLUMN location_id TEXT;
-- ISO country of location_id (empty for world regions), for "same country"
ALTER TABLE users ADD COLUMN location_country TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN discoverable BOOLEAN NOT NULL DEFAULT false;
CREATE INDEX idx_users_discoverable_country ON users (location_country) WHERE discoverable;

CREATE TABLE user_interests (
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    interest_id TEXT NOT NULL,
    PRIMARY KEY (user_id, interest_id)
);
CREATE INDEX idx_user_interests_interest ON user_interests (interest_id);

-- +goose Down
DROP TABLE user_interests;
DROP INDEX idx_users_discoverable_country;
ALTER TABLE users DROP COLUMN discoverable;
ALTER TABLE users DROP COLUMN location_country;
ALTER TABLE users DROP COLUMN location_id;
