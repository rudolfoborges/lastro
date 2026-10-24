-- +goose Up
ALTER TABLE transactions
    ADD COLUMN amount_basis text NOT NULL DEFAULT 'total' CHECK (amount_basis IN ('total', 'installment'));

CREATE TABLE installment_plans (
    id                       uuid PRIMARY KEY,
    account_id               uuid NOT NULL REFERENCES accounts (id),
    transaction_id           uuid NOT NULL,
    payment_modality         text NOT NULL CHECK (payment_modality IN ('card_installment', 'pix_installment')),
    installment_count        integer NOT NULL CHECK (installment_count BETWEEN 2 AND 120),
    currency                 char(3) NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
    installment_amount_minor bigint CHECK (installment_amount_minor > 0),
    installment_amount_known boolean NOT NULL DEFAULT false,
    total_minor              bigint CHECK (total_minor > 0),
    card_last4               text CHECK (card_last4 ~ '^[0-9]{4}$'),
    chain_key                text CHECK (char_length(chain_key) BETWEEN 1 AND 300),
    informed_on              date NOT NULL,
    canceled_at             timestamptz,
    created_at               timestamptz NOT NULL DEFAULT now(),
    updated_at               timestamptz NOT NULL DEFAULT now(),
    UNIQUE (account_id, id),
    FOREIGN KEY (account_id, transaction_id) REFERENCES transactions (account_id, id),
    CHECK (installment_amount_minor IS NOT NULL OR NOT installment_amount_known)
);
CREATE UNIQUE INDEX installment_plans_transaction_idx ON installment_plans (account_id, transaction_id) WHERE canceled_at IS NULL;
CREATE INDEX installment_plans_chain_idx ON installment_plans (account_id, chain_key, installment_count) WHERE canceled_at IS NULL;
ALTER TABLE installment_plans ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON installment_plans USING (account_id = current_account_id());

CREATE TABLE installments (
    id             uuid PRIMARY KEY,
    account_id     uuid NOT NULL REFERENCES accounts (id),
    plan_id        uuid NOT NULL,
    number         integer NOT NULL CHECK (number BETWEEN 1 AND 120),
    amount_minor   bigint NOT NULL CHECK (amount_minor > 0),
    state          text NOT NULL CHECK (state IN ('charged', 'known_future')),
    statement_id   uuid NOT NULL,
    observation_id uuid,
    informed_on    date NOT NULL,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    UNIQUE (plan_id, number),
    FOREIGN KEY (account_id, plan_id) REFERENCES installment_plans (account_id, id),
    FOREIGN KEY (account_id, statement_id) REFERENCES statements (account_id, id),
    FOREIGN KEY (account_id, observation_id) REFERENCES observations (account_id, id),
    CHECK ((state = 'charged') = (observation_id IS NOT NULL))
);
CREATE INDEX installments_observation_idx ON installments (account_id, observation_id) WHERE observation_id IS NOT NULL;
ALTER TABLE installments ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON installments USING (account_id = current_account_id());

-- +goose Down
DROP TABLE installments;
DROP TABLE installment_plans;
ALTER TABLE transactions DROP COLUMN amount_basis;
