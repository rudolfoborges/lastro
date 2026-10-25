-- +goose Up
CREATE TABLE export_links (
    id         uuid PRIMARY KEY,
    account_id uuid NOT NULL REFERENCES accounts (id),
    token_hash bytea NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
REVOKE ALL ON export_links FROM lastro_app;

-- +goose Down
DROP TABLE export_links;
