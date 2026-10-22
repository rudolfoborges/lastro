-- +goose Up
CREATE TABLE oauth_clients (
    id                  uuid PRIMARY KEY,
    client_id           text NOT NULL UNIQUE CHECK (char_length(client_id) BETWEEN 1 AND 500),
    registration        text NOT NULL CHECK (registration IN ('dynamic', 'metadata_document')),
    client_name         text CHECK (char_length(btrim(client_name)) BETWEEN 1 AND 200),
    redirect_uris       text[] NOT NULL CHECK (cardinality(redirect_uris) BETWEEN 1 AND 10),
    secret_hash         bytea,
    metadata_fetched_at timestamptz,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    CHECK ((registration = 'metadata_document') = (metadata_fetched_at IS NOT NULL)),
    CHECK (registration = 'dynamic' OR secret_hash IS NULL)
);
REVOKE ALL ON oauth_clients FROM lastro_app;
GRANT SELECT (id, client_id, client_name) ON oauth_clients TO lastro_app;

CREATE TABLE oauth_grants (
    id           uuid PRIMARY KEY,
    account_id   uuid NOT NULL REFERENCES accounts (id),
    client_id    uuid NOT NULL REFERENCES oauth_clients (id),
    api_key_id   uuid NOT NULL REFERENCES api_keys (id),
    scopes       text[] NOT NULL CHECK (
        cardinality(scopes) > 0
        AND scopes <@ ARRAY['expenses:read', 'expenses:write', 'statements:write']
    ),
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_used_at timestamptz,
    revoked_at   timestamptz,
    UNIQUE (account_id, id)
);
CREATE INDEX oauth_grants_account_id_idx ON oauth_grants (account_id);
ALTER TABLE oauth_grants ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON oauth_grants USING (account_id = current_account_id());
REVOKE INSERT ON oauth_grants FROM lastro_app;

CREATE TABLE oauth_authorization_codes (
    id             uuid PRIMARY KEY,
    code_hash      bytea NOT NULL UNIQUE,
    grant_id       uuid NOT NULL REFERENCES oauth_grants (id),
    client_id      uuid NOT NULL REFERENCES oauth_clients (id),
    redirect_uri   text NOT NULL,
    code_challenge text NOT NULL CHECK (code_challenge ~ '^[A-Za-z0-9_-]{43}$'),
    resource       text NOT NULL,
    expires_at     timestamptz NOT NULL,
    used_at        timestamptz,
    created_at     timestamptz NOT NULL DEFAULT now()
);
REVOKE ALL ON oauth_authorization_codes FROM lastro_app;

CREATE TABLE oauth_tokens (
    id         uuid PRIMARY KEY,
    grant_id   uuid NOT NULL REFERENCES oauth_grants (id),
    kind       text NOT NULL CHECK (kind IN ('access', 'refresh')),
    token_hash bytea NOT NULL,
    scopes     text[] NOT NULL CHECK (cardinality(scopes) > 0),
    resource   text NOT NULL,
    expires_at timestamptz NOT NULL,
    used_at    timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK (kind = 'refresh' OR used_at IS NULL)
);
CREATE INDEX oauth_tokens_grant_id_idx ON oauth_tokens (grant_id);
REVOKE ALL ON oauth_tokens FROM lastro_app;

ALTER TABLE api_requests ALTER COLUMN api_key_id DROP NOT NULL;
ALTER TABLE api_requests ADD COLUMN oauth_grant_id uuid REFERENCES oauth_grants (id);
ALTER TABLE api_requests ADD CONSTRAINT api_requests_one_credential CHECK (num_nonnulls(api_key_id, oauth_grant_id) = 1);

-- +goose Down
ALTER TABLE api_requests DROP CONSTRAINT api_requests_one_credential;
ALTER TABLE api_requests DROP COLUMN oauth_grant_id;
ALTER TABLE api_requests ALTER COLUMN api_key_id SET NOT NULL;
DROP TABLE oauth_tokens;
DROP TABLE oauth_authorization_codes;
DROP TABLE oauth_grants;
DROP TABLE oauth_clients;
