-- +goose Up
CREATE TABLE invites (
    id          uuid PRIMARY KEY,
    code_hash   bytea NOT NULL UNIQUE,
    note        text,
    created_at  timestamptz NOT NULL DEFAULT now(),
    expires_at  timestamptz NOT NULL,
    redeemed_at timestamptz,
    account_id  uuid REFERENCES accounts (id),
    CHECK ((redeemed_at IS NULL) = (account_id IS NULL))
);
REVOKE ALL ON invites FROM lastro_app;

CREATE TABLE api_keys (
    id           uuid PRIMARY KEY,
    account_id   uuid NOT NULL REFERENCES accounts (id),
    name         text NOT NULL CHECK (char_length(btrim(name)) BETWEEN 1 AND 100),
    token_hash   bytea NOT NULL,
    scopes       text[] NOT NULL CHECK (
        cardinality(scopes) > 0
        AND scopes <@ ARRAY['admin', 'expenses:read', 'expenses:write', 'wallet:ingest', 'statements:write']
    ),
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_used_at timestamptz,
    revoked_at   timestamptz
);
CREATE INDEX api_keys_account_id_idx ON api_keys (account_id);
ALTER TABLE api_keys ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON api_keys USING (account_id = current_account_id());

CREATE TABLE api_requests (
    id          uuid PRIMARY KEY,
    account_id  uuid NOT NULL REFERENCES accounts (id),
    api_key_id  uuid NOT NULL REFERENCES api_keys (id),
    request_id  text NOT NULL,
    method      text NOT NULL,
    route       text NOT NULL,
    status      integer NOT NULL,
    duration_ms integer NOT NULL CHECK (duration_ms >= 0),
    occurred_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX api_requests_account_id_occurred_at_idx ON api_requests (account_id, occurred_at);
ALTER TABLE api_requests ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON api_requests USING (account_id = current_account_id());
REVOKE UPDATE ON api_requests FROM lastro_app;

-- +goose Down
DROP TABLE api_requests;
DROP TABLE api_keys;
DROP TABLE invites;
