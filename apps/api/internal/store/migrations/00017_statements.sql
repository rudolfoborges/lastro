-- +goose Up
CREATE TABLE statements (
    id                uuid PRIMARY KEY,
    account_id        uuid NOT NULL REFERENCES accounts (id),
    issuer            text NOT NULL CHECK (char_length(btrim(issuer)) BETWEEN 1 AND 100),
    issuer_key        text NOT NULL CHECK (issuer_key ~ '^[a-z0-9]{1,100}$'),
    card_last4        text NOT NULL CHECK (card_last4 ~ '^[0-9]{4}$'),
    closing_date      date NOT NULL,
    payment_method_id uuid NOT NULL,
    created_at        timestamptz NOT NULL DEFAULT now(),
    UNIQUE (account_id, id),
    UNIQUE (account_id, issuer_key, card_last4, closing_date),
    FOREIGN KEY (account_id, payment_method_id) REFERENCES payment_methods (account_id, id)
);
ALTER TABLE statements ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON statements USING (account_id = current_account_id());

CREATE TABLE statement_versions (
    id                     uuid PRIMARY KEY,
    account_id             uuid NOT NULL REFERENCES accounts (id),
    statement_id           uuid NOT NULL,
    version                integer NOT NULL CHECK (version > 0),
    issue_date             date,
    due_date               date NOT NULL,
    period_start           date,
    period_end             date,
    currency               char(3) NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
    informed_total_minor   bigint NOT NULL,
    previous_balance_minor bigint,
    reading                text NOT NULL CHECK (reading IN ('complete', 'partial')),
    missing                text CHECK (char_length(btrim(missing)) BETWEEN 1 AND 500),
    page_count             integer CHECK (page_count BETWEEN 1 AND 500),
    file_sha256            bytea CHECK (octet_length(file_sha256) = 32),
    content_hash           bytea NOT NULL CHECK (octet_length(content_hash) = 32),
    created_at             timestamptz NOT NULL DEFAULT now(),
    UNIQUE (account_id, id),
    UNIQUE (statement_id, version),
    FOREIGN KEY (account_id, statement_id) REFERENCES statements (account_id, id),
    CHECK ((reading = 'partial') = (missing IS NOT NULL)),
    CHECK (period_start IS NULL OR period_end IS NULL OR period_start <= period_end)
);
ALTER TABLE statement_versions ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON statement_versions USING (account_id = current_account_id());
REVOKE UPDATE ON statement_versions FROM lastro_app;

CREATE TABLE statement_lines (
    id                   uuid PRIMARY KEY,
    account_id           uuid NOT NULL REFERENCES accounts (id),
    statement_version_id uuid NOT NULL,
    observation_id       uuid NOT NULL,
    position             integer NOT NULL CHECK (position > 0),
    kind                 text NOT NULL CHECK (kind IN (
        'purchase', 'installment', 'pix_installment', 'interest', 'fee', 'tax', 'credit', 'refund', 'payment', 'adjustment', 'unknown'
    )),
    direction            text NOT NULL CHECK (direction IN ('debit', 'credit')),
    amount_minor         bigint NOT NULL CHECK (amount_minor > 0),
    page                 integer NOT NULL CHECK (page BETWEEN 1 AND 500),
    excerpt              text NOT NULL CHECK (char_length(btrim(excerpt)) BETWEEN 1 AND 500),
    details              jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(details) = 'object'),
    UNIQUE (statement_version_id, observation_id),
    UNIQUE (statement_version_id, position),
    FOREIGN KEY (account_id, statement_version_id) REFERENCES statement_versions (account_id, id),
    FOREIGN KEY (account_id, observation_id) REFERENCES observations (account_id, id)
);
CREATE INDEX statement_lines_observation_idx ON statement_lines (account_id, observation_id);
ALTER TABLE statement_lines ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON statement_lines USING (account_id = current_account_id());
REVOKE UPDATE ON statement_lines FROM lastro_app;

ALTER TABLE clarifications
    ADD COLUMN statement_id uuid,
    ADD CONSTRAINT clarifications_statement_fkey FOREIGN KEY (account_id, statement_id) REFERENCES statements (account_id, id),
    DROP CONSTRAINT clarifications_check,
    ADD CONSTRAINT clarifications_target_check CHECK (num_nonnulls(transaction_id, observation_id, statement_id) > 0);
CREATE INDEX clarifications_statement_idx ON clarifications (account_id, statement_id) WHERE status = 'open';

-- +goose Down
DROP INDEX clarifications_statement_idx;
DELETE FROM clarifications WHERE transaction_id IS NULL AND observation_id IS NULL;
ALTER TABLE clarifications
    DROP CONSTRAINT clarifications_target_check,
    ADD CONSTRAINT clarifications_check CHECK (transaction_id IS NOT NULL OR observation_id IS NOT NULL),
    DROP CONSTRAINT clarifications_statement_fkey,
    DROP COLUMN statement_id;
DROP TABLE statement_lines;
DROP TABLE statement_versions;
DROP TABLE statements;
