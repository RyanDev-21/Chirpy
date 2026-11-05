-- +goose Up
-- 1:1 voice calls: one row per call attempt (the call log). The audio goes
-- phone to phone (or through the TURN relay) and is never stored.
CREATE TABLE calls (
    id          UUID PRIMARY KEY,
    caller_id   UUID REFERENCES users(id) ON DELETE SET NULL,
    callee_id   UUID REFERENCES users(id) ON DELETE SET NULL,
    state       TEXT NOT NULL CHECK (state IN ('ringing', 'active', 'ended', 'missed', 'declined', 'cancelled', 'busy', 'failed')),
    end_reason  TEXT,
    started_at  TIMESTAMP NOT NULL,
    answered_at TIMESTAMP,
    ended_at    TIMESTAMP
);
CREATE INDEX idx_calls_caller ON calls (caller_id, started_at DESC, id DESC);
CREATE INDEX idx_calls_callee ON calls (callee_id, started_at DESC, id DESC);

-- +goose Down
DROP TABLE calls;
