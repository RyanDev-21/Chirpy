-- +goose Up
-- Groups are joined with an invite code: 8 characters from an alphabet
-- without look-alikes (no 0/O, 1/I), about 40 bits, so codes can't be
-- guessed. Existing groups get one now.
ALTER TABLE chat_groups ADD COLUMN invite_code TEXT;
UPDATE chat_groups g
SET invite_code = (
    SELECT string_agg(substr('ABCDEFGHJKLMNPQRSTUVWXYZ23456789', 1 + floor(random() * 32)::int, 1), '')
    -- referencing g.id makes postgres evaluate this once per row
    FROM generate_series(1, 8 + 0 * length(g.id::text))
);
ALTER TABLE chat_groups ALTER COLUMN invite_code SET NOT NULL;
ALTER TABLE chat_groups ADD CONSTRAINT chat_groups_invite_code_key UNIQUE (invite_code);

-- +goose Down
ALTER TABLE chat_groups DROP CONSTRAINT chat_groups_invite_code_key;
ALTER TABLE chat_groups DROP COLUMN invite_code;
