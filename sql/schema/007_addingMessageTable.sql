-- +goose Up
CREATE TABLE message(
   id UUID PRIMARY KEY NOT NULL,
   content VARCHAR(500),
   parentId UUID REFERENCES message(id) ON DELETE SET NULL, 
   from_id UUID REFERENCES users(id)ON DELETE SET NULL,
   to_id UUID REFERENCES users(id)ON DELETE SET NULL,
   --  soft delete
    deleted_at TIMESTAMP DEFAULT NULL,
   created_at TIMESTAMP NOT NULL DEFAULT NOW(),
   updated_at TIMESTAMP NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_1to1_chat ON message (from_id, to_id, created_at DESC);
CREATE INDEX idx_message_receiver ON message (to_id, created_at DESC);


CREATE TABLE chat_groups(
    id UUID PRIMARY KEY NOT NULL,
    name VARCHAR(30),
    description VARCHAR(1000),
    max_member smallint, 
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW()
);

CREATE TABLE GroupMessage(
    id UUID PRIMARY KEY NOT NULL,
    content VARCHAR(500),
    group_id UUID REFERENCES chat_groups(id) ON DELETE CASCADE,
    from_id UUID REFERENCES users(id)ON DELETE SET NULL,
    parent_id UUID REFERENCES GroupMessage(id) DEFAULT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMP DEFAULT NULL
);
--making the group_id and created_at indexes so that i can get the message of the gorup quickly
CREATE INDEX idx_message_group ON GroupMessage (group_id, created_at DESC);
--making the parent_id as index so that i can serach the child of the parent message
CREATE INDEX idx_gm_parent ON GroupMessage(parent_id);



CREATE TABLE member_table(
    id UUID PRIMARY KEY NOT NULL,
    group_id UUID REFERENCES chat_groups(id) ON DELETE CASCADE,
    --this kinda make sense to jsut delete the user member record of the group
    member_id UUID REFERENCES users(id) ON DELETE CASCADE,
    joined_at TIMESTAMP NOT NULL DEFAULT NOW(),
    role VARCHAR(20) DEFAULT 'member',
    UNIQUE(group_id,member_id)
 );

-- +goose Down
DROP TABLE message;
DROP TABLE GroupMessage;
DROP TABLE chat_groups;
DROP TABLE member_table;




