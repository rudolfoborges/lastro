-- +goose Up
CREATE TABLE accounts (
    id         uuid PRIMARY KEY,
    name       text NOT NULL CHECK (char_length(btrim(name)) BETWEEN 1 AND 100),
    created_at timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE accounts ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON accounts USING (id = current_account_id());

-- +goose Down
DROP TABLE accounts;
