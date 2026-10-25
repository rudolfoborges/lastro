-- +goose Up
ALTER TABLE classification_rules
    ADD COLUMN min_amount_minor bigint CHECK (min_amount_minor >= 0),
    ADD COLUMN max_amount_minor bigint CHECK (max_amount_minor > 0),
    ADD COLUMN currency         text CHECK (currency ~ '^[A-Z]{3}$'),
    ADD COLUMN card_last4       text CHECK (card_last4 ~ '^[0-9]{4}$'),
    ADD COLUMN payment_method   text CHECK (payment_method IN ('credit_card', 'debit_card', 'pix', 'cash')),
    ADD COLUMN conditions_key   text NOT NULL DEFAULT '',
    ADD COLUMN rationale        text CHECK (char_length(btrim(rationale)) BETWEEN 1 AND 500),
    ADD COLUMN based_on         uuid[] CHECK (cardinality(based_on) BETWEEN 2 AND 50),
    ADD COLUMN approved_at      timestamptz;

UPDATE classification_rules SET approved_at = created_at;

ALTER TABLE classification_rules ALTER COLUMN instruction DROP NOT NULL;
ALTER TABLE classification_rules ALTER COLUMN status SET DEFAULT 'suggested';
ALTER TABLE classification_rules DROP CONSTRAINT classification_rules_status_check;
ALTER TABLE classification_rules
    ADD CONSTRAINT classification_rules_status_check CHECK (status IN ('suggested', 'active', 'archived')),
    ADD CONSTRAINT classification_rules_amount_range CHECK (min_amount_minor IS NULL OR max_amount_minor IS NULL OR min_amount_minor < max_amount_minor),
    ADD CONSTRAINT classification_rules_amount_currency CHECK ((min_amount_minor IS NULL AND max_amount_minor IS NULL) = (currency IS NULL)),
    ADD CONSTRAINT classification_rules_approval CHECK ((approved_at IS NULL) = (instruction IS NULL)),
    ADD CONSTRAINT classification_rules_active_approved CHECK (status <> 'active' OR approved_at IS NOT NULL),
    ADD CONSTRAINT classification_rules_suggestion CHECK (approved_at IS NOT NULL OR (rationale IS NOT NULL AND based_on IS NOT NULL));

DROP INDEX classification_rules_active_idx;
CREATE UNIQUE INDEX classification_rules_active_idx ON classification_rules (account_id, merchant_key, conditions_key) WHERE status = 'active';
CREATE UNIQUE INDEX classification_rules_suggested_idx ON classification_rules (account_id, merchant_key, conditions_key) WHERE status = 'suggested';

-- +goose Down
DROP INDEX classification_rules_suggested_idx;
DROP INDEX classification_rules_active_idx;
DELETE FROM classification_rules WHERE approved_at IS NULL;
CREATE UNIQUE INDEX classification_rules_active_idx ON classification_rules (account_id, merchant_key) WHERE status = 'active';
ALTER TABLE classification_rules
    DROP CONSTRAINT classification_rules_suggestion,
    DROP CONSTRAINT classification_rules_active_approved,
    DROP CONSTRAINT classification_rules_approval,
    DROP CONSTRAINT classification_rules_amount_currency,
    DROP CONSTRAINT classification_rules_amount_range,
    DROP CONSTRAINT classification_rules_status_check;
ALTER TABLE classification_rules ADD CONSTRAINT classification_rules_status_check CHECK (status IN ('active', 'archived'));
ALTER TABLE classification_rules ALTER COLUMN status SET DEFAULT 'active';
ALTER TABLE classification_rules ALTER COLUMN instruction SET NOT NULL;
ALTER TABLE classification_rules
    DROP COLUMN approved_at,
    DROP COLUMN based_on,
    DROP COLUMN rationale,
    DROP COLUMN conditions_key,
    DROP COLUMN payment_method,
    DROP COLUMN card_last4,
    DROP COLUMN currency,
    DROP COLUMN max_amount_minor,
    DROP COLUMN min_amount_minor;
