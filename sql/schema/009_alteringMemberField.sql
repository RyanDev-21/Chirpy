-- +goose Up
ALTER TABLE member_table 
ALTER COLUMN group_id SET NOT NULL,
ALTER COLUMN member_id SET NOT NULL,
ALTER COLUMN id SET DEFAULT gen_random_uuid(),
ALTER COLUMN role SET NOT NULL;

ALTER TABLE chat_groups
ADD COLUMN current_member smallint NOT NULL DEFAULT 0;

ALTER TABLE chat_groups ADD CONSTRAINT unique_name UNIQUE(name);

-- +goose Down
ALTER TABLE member_table
ALTER COLUMN group_id DROP NOT NULL,
ALTER COLUMN member_id DROP NOT NULL,
ALTER COLUMN id DROP DEFAULT,
ALTER COLUMN role DROP NOT NULL;


ALTER TABLE chat_groups
DROP COLUMN current_member;
ALTER TABLE chat_groups  DROP CONSTRAINT unique_name;
