-- +goose Up
-- Audit clean-up (2026-11-05).

-- BUG: the case-sensitive unique name from 009 was still there after 043
-- made names non-unique: a second group called exactly "devs" failed (500)
ALTER TABLE chat_groups DROP CONSTRAINT IF EXISTS unique_name;

-- leftovers from the course project Chirpy started as: never read or set
DROP TABLE IF EXISTS chirp;
ALTER TABLE users DROP COLUMN is_chirpy_red;
ALTER TABLE user_relationships DROP COLUMN label;

-- written but never read: edited_at and deleted_at say what changed
ALTER TABLE message DROP COLUMN updated_at;
ALTER TABLE GroupMessage DROP COLUMN updated_at;
ALTER TABLE seen_messages DROP COLUMN updated_time;

-- surrogate ids nothing uses: the natural pair becomes the primary key
-- (one index less per row on the two busiest small tables)
ALTER TABLE member_table DROP CONSTRAINT member_table_pkey;
ALTER TABLE member_table DROP COLUMN id;
ALTER TABLE member_table DROP CONSTRAINT member_table_group_id_member_id_key;
ALTER TABLE member_table ADD PRIMARY KEY (group_id, member_id);

DELETE FROM seen_messages WHERE seen_id IS NULL;
ALTER TABLE seen_messages ALTER COLUMN seen_id SET NOT NULL;
ALTER TABLE seen_messages DROP CONSTRAINT seen_messages_pkey;
ALTER TABLE seen_messages DROP COLUMN id;
ALTER TABLE seen_messages DROP CONSTRAINT seen_messages_chat_seen_key;
ALTER TABLE seen_messages ADD PRIMARY KEY (chat_id, seen_id);

-- +goose Down
ALTER TABLE seen_messages DROP CONSTRAINT seen_messages_pkey;
ALTER TABLE seen_messages ADD CONSTRAINT seen_messages_chat_seen_key UNIQUE (chat_id, seen_id);
ALTER TABLE seen_messages ADD COLUMN id UUID NOT NULL DEFAULT gen_random_uuid() PRIMARY KEY;
ALTER TABLE seen_messages ALTER COLUMN seen_id DROP NOT NULL;
ALTER TABLE member_table DROP CONSTRAINT member_table_pkey;
ALTER TABLE member_table ADD CONSTRAINT member_table_group_id_member_id_key UNIQUE (group_id, member_id);
ALTER TABLE member_table ADD COLUMN id UUID NOT NULL DEFAULT gen_random_uuid() PRIMARY KEY;
ALTER TABLE seen_messages ADD COLUMN updated_time TIMESTAMP NOT NULL DEFAULT NOW();
ALTER TABLE GroupMessage ADD COLUMN updated_at TIMESTAMP NOT NULL DEFAULT NOW();
ALTER TABLE message ADD COLUMN updated_at TIMESTAMP NOT NULL DEFAULT NOW();
ALTER TABLE user_relationships ADD COLUMN label VARCHAR(20) NOT NULL DEFAULT 'friend';
ALTER TABLE users ADD COLUMN is_chirpy_red BOOLEAN DEFAULT false;
