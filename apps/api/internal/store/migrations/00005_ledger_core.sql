-- +goose Up
CREATE TABLE payment_methods (
    id         uuid PRIMARY KEY,
    account_id uuid NOT NULL REFERENCES accounts (id),
    kind       text NOT NULL CHECK (kind IN ('credit_card', 'debit_card', 'pix', 'cash')),
    card_last4 text CHECK (card_last4 ~ '^[0-9]{4}$'),
    issuer     text CHECK (char_length(btrim(issuer)) BETWEEN 1 AND 100),
    network    text CHECK (char_length(btrim(network)) BETWEEN 1 AND 50),
    nickname   text CHECK (char_length(btrim(nickname)) BETWEEN 1 AND 100),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (account_id, id),
    CHECK (card_last4 IS NULL OR kind IN ('credit_card', 'debit_card'))
);
CREATE UNIQUE INDEX payment_methods_identity_idx ON payment_methods (account_id, kind, coalesce(card_last4, ''));
ALTER TABLE payment_methods ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON payment_methods USING (account_id = current_account_id());

CREATE TABLE observations (
    id          uuid PRIMARY KEY,
    account_id  uuid NOT NULL REFERENCES accounts (id),
    source      text NOT NULL CHECK (source IN ('conversation', 'wallet_notification', 'statement_line', 'manual', 'import')),
    source_key  text NOT NULL CHECK (char_length(source_key) BETWEEN 1 AND 300),
    payload     jsonb NOT NULL CHECK (jsonb_typeof(payload) = 'object'),
    redactions  jsonb NOT NULL DEFAULT '[]' CHECK (jsonb_typeof(redactions) = 'array'),
    fingerprint bytea,
    received_at timestamptz NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (account_id, id),
    UNIQUE (account_id, source, source_key)
);
CREATE INDEX observations_fingerprint_idx ON observations (account_id, source, fingerprint, received_at)
    WHERE fingerprint IS NOT NULL;
ALTER TABLE observations ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON observations USING (account_id = current_account_id());
REVOKE UPDATE ON observations FROM lastro_app;

CREATE TABLE observation_interpretations (
    id                uuid PRIMARY KEY,
    account_id        uuid NOT NULL REFERENCES accounts (id),
    observation_id    uuid NOT NULL,
    version           integer NOT NULL CHECK (version > 0),
    event_type        text NOT NULL CHECK (event_type IN ('purchase', 'purchase_update', 'cancellation', 'refund', 'declined', 'notice', 'unrelated')),
    amount_minor      bigint,
    currency          char(3) CHECK (currency ~ '^[A-Z]{3}$'),
    merchant          text CHECK (char_length(btrim(merchant)) BETWEEN 1 AND 200),
    occurred_at       timestamptz,
    occurred_on       date,
    date_precision    text NOT NULL CHECK (date_precision IN ('exact', 'day', 'month', 'unknown')),
    date_source       text NOT NULL CHECK (date_source IN ('stated', 'inferred', 'document')),
    payment_method_id uuid,
    details           jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(details) = 'object'),
    author            text NOT NULL CHECK (author IN ('agent', 'user', 'system')),
    created_at        timestamptz NOT NULL DEFAULT now(),
    UNIQUE (observation_id, version),
    FOREIGN KEY (account_id, observation_id) REFERENCES observations (account_id, id),
    FOREIGN KEY (account_id, payment_method_id) REFERENCES payment_methods (account_id, id),
    CHECK ((amount_minor IS NULL) = (currency IS NULL)),
    CHECK (date_precision <> 'exact' OR occurred_at IS NOT NULL),
    CHECK (date_precision = 'exact' OR occurred_at IS NULL),
    CHECK ((date_precision = 'unknown') = (occurred_on IS NULL))
);
ALTER TABLE observation_interpretations ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON observation_interpretations USING (account_id = current_account_id());
REVOKE UPDATE ON observation_interpretations FROM lastro_app;

CREATE TABLE transactions (
    id                       uuid PRIMARY KEY,
    account_id               uuid NOT NULL REFERENCES accounts (id),
    original_description     text NOT NULL CHECK (char_length(original_description) BETWEEN 1 AND 2000),
    description              text NOT NULL CHECK (char_length(btrim(description)) BETWEEN 1 AND 200),
    merchant                 text CHECK (char_length(btrim(merchant)) BETWEEN 1 AND 200),
    payee                    text CHECK (char_length(btrim(payee)) BETWEEN 1 AND 200),
    amount_minor             bigint NOT NULL CHECK (amount_minor > 0),
    currency                 char(3) NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
    original_amount_minor    bigint CHECK (original_amount_minor > 0),
    original_currency        char(3) CHECK (original_currency ~ '^[A-Z]{3}$'),
    occurred_at              timestamptz,
    occurred_on              date,
    date_precision           text NOT NULL CHECK (date_precision IN ('exact', 'day', 'month', 'unknown')),
    date_source              text NOT NULL CHECK (date_source IN ('stated', 'inferred', 'document')),
    payment_method_id        uuid,
    nature                   text NOT NULL CHECK (nature IN ('expense', 'charge', 'credit', 'refund', 'payment', 'transfer', 'adjustment')),
    status                   text NOT NULL CHECK (status IN ('provisional', 'confirmed', 'pending_clarification', 'voided', 'merged')),
    purpose                  text CHECK (char_length(btrim(purpose)) BETWEEN 1 AND 200),
    installment_count        integer CHECK (installment_count BETWEEN 2 AND 120),
    installment_amount_minor bigint CHECK (installment_amount_minor > 0),
    refund_of_id             uuid,
    merged_into_id           uuid,
    created_at               timestamptz NOT NULL DEFAULT now(),
    updated_at               timestamptz NOT NULL DEFAULT now(),
    UNIQUE (account_id, id),
    FOREIGN KEY (account_id, payment_method_id) REFERENCES payment_methods (account_id, id),
    FOREIGN KEY (account_id, refund_of_id) REFERENCES transactions (account_id, id),
    FOREIGN KEY (account_id, merged_into_id) REFERENCES transactions (account_id, id),
    CHECK ((status = 'merged') = (merged_into_id IS NOT NULL)),
    CHECK (merged_into_id <> id),
    CHECK (refund_of_id <> id),
    CHECK ((original_amount_minor IS NULL) = (original_currency IS NULL)),
    CHECK (date_precision <> 'exact' OR occurred_at IS NOT NULL),
    CHECK (date_precision = 'exact' OR occurred_at IS NULL),
    CHECK ((date_precision = 'unknown') = (occurred_on IS NULL)),
    CHECK (date_precision <> 'month' OR extract(day FROM occurred_on) = 1),
    CHECK (installment_amount_minor IS NULL OR installment_count IS NOT NULL)
);
CREATE INDEX transactions_account_occurred_on_idx ON transactions (account_id, occurred_on);
CREATE INDEX transactions_account_created_at_idx ON transactions (account_id, created_at);
ALTER TABLE transactions ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON transactions USING (account_id = current_account_id());

CREATE TABLE transaction_evidence (
    id             uuid PRIMARY KEY,
    account_id     uuid NOT NULL REFERENCES accounts (id),
    transaction_id uuid NOT NULL,
    observation_id uuid NOT NULL,
    status         text NOT NULL CHECK (status IN ('suggested', 'confirmed', 'rejected', 'undone')),
    origin         text NOT NULL CHECK (origin IN ('system', 'agent', 'user')),
    score          smallint CHECK (score BETWEEN 0 AND 100),
    reasons        text[] NOT NULL DEFAULT '{}',
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (account_id, transaction_id) REFERENCES transactions (account_id, id),
    FOREIGN KEY (account_id, observation_id) REFERENCES observations (account_id, id)
);
CREATE UNIQUE INDEX transaction_evidence_open_link_idx ON transaction_evidence (transaction_id, observation_id)
    WHERE status IN ('suggested', 'confirmed');
CREATE UNIQUE INDEX transaction_evidence_confirmed_observation_idx ON transaction_evidence (observation_id)
    WHERE status = 'confirmed';
CREATE INDEX transaction_evidence_account_transaction_idx ON transaction_evidence (account_id, transaction_id);
ALTER TABLE transaction_evidence ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON transaction_evidence USING (account_id = current_account_id());

CREATE TABLE field_provenance (
    account_id        uuid NOT NULL REFERENCES accounts (id),
    transaction_id    uuid NOT NULL,
    field             text NOT NULL CHECK (field IN ('description', 'merchant', 'payee', 'amount', 'occurred', 'payment_method', 'nature', 'purpose', 'installments')),
    observation_id    uuid,
    confirmed_by_user boolean NOT NULL DEFAULT false,
    updated_at        timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (transaction_id, field),
    FOREIGN KEY (account_id, transaction_id) REFERENCES transactions (account_id, id),
    FOREIGN KEY (account_id, observation_id) REFERENCES observations (account_id, id)
);
ALTER TABLE field_provenance ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON field_provenance USING (account_id = current_account_id());

-- +goose Down
DROP TABLE field_provenance;
DROP TABLE transaction_evidence;
DROP TABLE transactions;
DROP TABLE observation_interpretations;
DROP TABLE observations;
DROP TABLE payment_methods;
