-- +goose Up
CREATE TABLE idempotency_keys (
    account_id   uuid NOT NULL REFERENCES accounts (id),
    operation    text NOT NULL CHECK (operation ~ '^[a-z][a-z0-9_]{0,63}$'),
    key          text NOT NULL CHECK (char_length(key) BETWEEN 1 AND 200),
    request_hash bytea NOT NULL,
    response     jsonb NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (account_id, operation, key)
);
ALTER TABLE idempotency_keys ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON idempotency_keys USING (account_id = current_account_id());
REVOKE UPDATE ON idempotency_keys FROM lastro_app;

-- +goose Down
DROP TABLE idempotency_keys;
