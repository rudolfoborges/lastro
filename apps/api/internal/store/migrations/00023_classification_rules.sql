-- +goose Up
CREATE TABLE classification_rules (
    id              uuid PRIMARY KEY,
    account_id      uuid NOT NULL REFERENCES accounts (id),
    merchant        text NOT NULL CHECK (char_length(btrim(merchant)) BETWEEN 1 AND 200),
    merchant_key    text NOT NULL CHECK (char_length(merchant_key) BETWEEN 1 AND 200),
    category_id     uuid NOT NULL,
    purpose         text CHECK (char_length(btrim(purpose)) BETWEEN 1 AND 200),
    instruction     text NOT NULL CHECK (char_length(btrim(instruction)) BETWEEN 1 AND 500),
    status          text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'archived')),
    archived_reason text CHECK (char_length(btrim(archived_reason)) BETWEEN 1 AND 500),
    created_at      timestamptz NOT NULL DEFAULT now(),
    archived_at     timestamptz,
    UNIQUE (account_id, id),
    FOREIGN KEY (account_id, category_id) REFERENCES categories (account_id, id),
    CHECK ((status = 'archived') = (archived_at IS NOT NULL)),
    CHECK ((status = 'archived') = (archived_reason IS NOT NULL))
);
CREATE UNIQUE INDEX classification_rules_active_idx ON classification_rules (account_id, merchant_key) WHERE status = 'active';
ALTER TABLE classification_rules ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON classification_rules USING (account_id = current_account_id());

-- +goose Down
DROP TABLE classification_rules;
