-- +goose Up
-- `name` becomes the username (handle): lowercase a-z 0-9 _ . (3-25),
-- unique. `display_name` is what people see: free text, not unique.
-- Existing names are kept as display names, and converted to usernames
-- (oldest account first; a number is added when the result is taken).
ALTER TABLE users ADD COLUMN display_name VARCHAR(50) NOT NULL DEFAULT '';
UPDATE users SET display_name = left(name, 50);

-- +goose StatementBegin
DO $$
DECLARE
    r    record;
    base text;
    cand text;
    n    int;
BEGIN
    FOR r IN SELECT id, name FROM users ORDER BY created_at, id LOOP
        base := lower(regexp_replace(r.name, '[^A-Za-z0-9_.]', '', 'g'));
        IF length(base) < 3 THEN
            base := 'user' || substr(replace(r.id::text, '-', ''), 1, 6);
        END IF;
        base := left(base, 22);
        cand := base;
        n := 1;
        WHILE EXISTS (SELECT 1 FROM users WHERE name = cand AND id <> r.id) LOOP
            n := n + 1;
            cand := base || n::text;
        END LOOP;
        IF cand <> r.name THEN
            UPDATE users SET name = cand, updated_at = NOW() WHERE id = r.id;
        END IF;
    END LOOP;
END $$;
-- +goose StatementEnd

ALTER TABLE users ADD CONSTRAINT users_name_format CHECK (name ~ '^[a-z0-9_.]{3,25}$');
CREATE INDEX trgm_display_name_idx ON users USING GIST (display_name gist_trgm_ops(siglen=32));

-- +goose Down
DROP INDEX trgm_display_name_idx;
ALTER TABLE users DROP CONSTRAINT users_name_format;
ALTER TABLE users DROP COLUMN display_name;
