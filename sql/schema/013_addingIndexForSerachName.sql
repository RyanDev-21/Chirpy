-- +goose Up
CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE INDEX trgm_idx ON users USING GIST (name gist_trgm_ops(siglen=32));



-- +goose Down
DROP INDEX IF EXISTS trgm_idx;
