-- +goose Up
CREATE TABLE categories (
    id          uuid PRIMARY KEY,
    account_id  uuid NOT NULL REFERENCES accounts (id),
    code        text NOT NULL CHECK (code ~ '^[a-z][a-z0-9_]{0,49}$'),
    label       text NOT NULL CHECK (char_length(btrim(label)) BETWEEN 1 AND 100),
    archived_at timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (account_id, id),
    UNIQUE (account_id, code)
);
ALTER TABLE categories ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON categories USING (account_id = current_account_id());

CREATE TABLE category_allocations (
    id             uuid PRIMARY KEY,
    account_id     uuid NOT NULL REFERENCES accounts (id),
    transaction_id uuid NOT NULL,
    category_id    uuid NOT NULL,
    amount_minor   bigint NOT NULL CHECK (amount_minor > 0),
    status         text NOT NULL CHECK (status IN ('suggested', 'confirmed')),
    origin         text NOT NULL CHECK (origin IN ('agent', 'rule', 'user')),
    superseded_at  timestamptz,
    created_at     timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (account_id, transaction_id) REFERENCES transactions (account_id, id),
    FOREIGN KEY (account_id, category_id) REFERENCES categories (account_id, id)
);
CREATE UNIQUE INDEX category_allocations_current_idx ON category_allocations (transaction_id, category_id)
    WHERE superseded_at IS NULL;
CREATE INDEX category_allocations_account_transaction_idx ON category_allocations (account_id, transaction_id)
    WHERE superseded_at IS NULL;
ALTER TABLE category_allocations ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON category_allocations USING (account_id = current_account_id());

-- The service validates splits with clear messages; these deferred triggers are
-- the backstop that keeps current allocations equal to the transaction amount
-- at commit time, whichever code path changed either side.
-- +goose StatementBegin
CREATE FUNCTION check_category_allocations() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    target    uuid;
    allocated bigint;
    expected  bigint;
BEGIN
    IF TG_TABLE_NAME = 'transactions' THEN
        target := NEW.id;
    ELSE
        target := NEW.transaction_id;
    END IF;
    SELECT sum(amount_minor) INTO allocated
    FROM category_allocations
    WHERE transaction_id = target AND superseded_at IS NULL;
    IF allocated IS NULL THEN
        RETURN NULL;
    END IF;
    SELECT amount_minor INTO expected FROM transactions WHERE id = target;
    IF allocated <> expected THEN
        RAISE EXCEPTION 'category allocations of transaction % add up to % instead of %', target, allocated, expected
            USING ERRCODE = 'check_violation', CONSTRAINT = 'category_allocations_match_amount';
    END IF;
    RETURN NULL;
END
$$;
-- +goose StatementEnd

CREATE CONSTRAINT TRIGGER category_allocations_match_amount
    AFTER INSERT OR UPDATE ON category_allocations
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION check_category_allocations();

CREATE CONSTRAINT TRIGGER transactions_match_category_allocations
    AFTER UPDATE OF amount_minor ON transactions
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION check_category_allocations();

-- Accounts created before this migration get the default taxonomy; new
-- accounts receive it at sign-up with application-generated IDs.
INSERT INTO categories (id, account_id, code, label)
SELECT uuidv7(), accounts.id, defaults.code, defaults.label
FROM accounts
CROSS JOIN (VALUES
    ('groceries', 'Mercado'),
    ('meals', 'Alimentação e refeições'),
    ('convenience_snacks', 'Conveniência — bebidas e snacks'),
    ('fuel', 'Combustível'),
    ('transport', 'Transporte'),
    ('home', 'Casa e reforma'),
    ('health', 'Saúde'),
    ('education', 'Educação'),
    ('clothing', 'Vestuário'),
    ('electronics', 'Eletrônicos'),
    ('software_subscriptions', 'Software e assinaturas'),
    ('leisure', 'Lazer'),
    ('financial_charges', 'Encargos financeiros'),
    ('unidentified', 'Não identificado')
) AS defaults (code, label);

-- +goose Down
DROP TRIGGER transactions_match_category_allocations ON transactions;
DROP TABLE category_allocations;
DROP FUNCTION check_category_allocations();
DROP TABLE categories;
