-- +goose Up
CREATE TABLE clarifications (
    id             uuid PRIMARY KEY,
    account_id     uuid NOT NULL REFERENCES accounts (id),
    kind           text NOT NULL CHECK (kind IN (
        'purpose_unknown', 'category_unknown', 'possible_match', 'reading_doubt', 'unmatched_notification',
        'statement_version_diff', 'statement_divergence', 'missing_breakdown', 'field_conflict'
    )),
    transaction_id uuid,
    observation_id uuid,
    subject        text NOT NULL CHECK (subject ~ '^[a-z0-9_:-]{1,100}$'),
    dedup_key      text NOT NULL CHECK (char_length(dedup_key) BETWEEN 1 AND 300),
    question       text NOT NULL CHECK (char_length(btrim(question)) BETWEEN 1 AND 500),
    context        jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(context) = 'object'),
    status         text NOT NULL CHECK (status IN ('open', 'resolved', 'dismissed')),
    resolution     text CHECK (char_length(btrim(resolution)) BETWEEN 1 AND 500),
    resolved_by    text CHECK (resolved_by IN ('user', 'agent', 'system')),
    resolved_at    timestamptz,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    UNIQUE (account_id, id),
    FOREIGN KEY (account_id, transaction_id) REFERENCES transactions (account_id, id),
    FOREIGN KEY (account_id, observation_id) REFERENCES observations (account_id, id),
    CHECK (transaction_id IS NOT NULL OR observation_id IS NOT NULL),
    CHECK ((status = 'open') = (resolved_at IS NULL)),
    CHECK ((status = 'open') = (resolved_by IS NULL))
);
CREATE UNIQUE INDEX clarifications_open_dedup_idx ON clarifications (account_id, dedup_key) WHERE status = 'open';
CREATE INDEX clarifications_account_status_idx ON clarifications (account_id, status, created_at);
CREATE INDEX clarifications_transaction_idx ON clarifications (account_id, transaction_id) WHERE status = 'open';
ALTER TABLE clarifications ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON clarifications USING (account_id = current_account_id());

-- +goose Down
DROP TABLE clarifications;
