-- +goose Up
-- A user reports another user. Evidence is only what the reporter chose
-- to attach: messages from a chat they're in, copied at report time (the
-- reporter has seen them already). Admins see nothing else.
CREATE TABLE reports (
    id           UUID PRIMARY KEY,
    reporter_id  UUID REFERENCES users(id) ON DELETE SET NULL,
    reported_id  UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    reason       TEXT NOT NULL CHECK (reason IN ('spam', 'harassment', 'hate', 'violence', 'sexual', 'impersonation', 'scam', 'other')),
    note         TEXT NOT NULL DEFAULT '' CHECK (length(note) <= 4000),
    status       TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'resolved', 'dismissed')),
    resolved_by  UUID REFERENCES users(id) ON DELETE SET NULL,
    resolved_at  TIMESTAMP,
    resolution   TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMP NOT NULL
);
CREATE INDEX idx_reports_status ON reports (status, created_at DESC);
CREATE INDEX idx_reports_reported ON reports (reported_id);
-- at most one open report per reporter and reported user (a new report
-- updates it instead)
CREATE UNIQUE INDEX reports_one_open ON reports (reporter_id, reported_id) WHERE status = 'open';

CREATE TABLE report_messages (
    report_id   UUID NOT NULL REFERENCES reports(id) ON DELETE CASCADE,
    message_id  UUID NOT NULL,
    chat_type   TEXT NOT NULL CHECK (chat_type IN ('private', 'public')),
    group_id    UUID,
    sender_id   UUID,
    content     TEXT NOT NULL,
    sent_at     TIMESTAMP NOT NULL,
    PRIMARY KEY (report_id, message_id)
);

-- +goose Down
DROP TABLE report_messages;
DROP TABLE reports;
