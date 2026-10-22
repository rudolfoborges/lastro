-- +goose Up
CREATE TABLE tool_calls (
    id              uuid PRIMARY KEY,
    account_id      uuid NOT NULL REFERENCES accounts (id),
    credential_kind text NOT NULL CHECK (credential_kind IN ('api_key', 'oauth_grant')),
    credential_id   uuid NOT NULL,
    tool            text NOT NULL CHECK (tool ~ '^[a-z][a-z0-9_]{0,63}$'),
    outcome         text NOT NULL CHECK (outcome IN ('completed', 'pending', 'failed')),
    error_code      text CHECK (error_code ~ '^[a-z][a-z0-9_]{0,63}$'),
    duration_ms     integer NOT NULL CHECK (duration_ms >= 0),
    occurred_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX tool_calls_account_occurred_at_idx ON tool_calls (account_id, occurred_at);
ALTER TABLE tool_calls ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON tool_calls USING (account_id = current_account_id());
REVOKE UPDATE ON tool_calls FROM lastro_app;

-- +goose Down
DROP TABLE tool_calls;
