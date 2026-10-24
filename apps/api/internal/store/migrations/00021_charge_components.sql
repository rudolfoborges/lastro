-- +goose Up
CREATE TABLE charge_components (
    id             uuid PRIMARY KEY,
    account_id     uuid NOT NULL REFERENCES accounts (id),
    observation_id uuid NOT NULL,
    statement_id   uuid NOT NULL,
    kind           text NOT NULL CHECK (kind IN ('principal', 'interest', 'iof', 'fee')),
    amount_minor   bigint NOT NULL CHECK (amount_minor > 0),
    currency       char(3) NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
    included       boolean NOT NULL,
    created_at     timestamptz NOT NULL DEFAULT now(),
    UNIQUE (observation_id, kind),
    FOREIGN KEY (account_id, observation_id) REFERENCES observations (account_id, id),
    FOREIGN KEY (account_id, statement_id) REFERENCES statements (account_id, id)
);
CREATE INDEX charge_components_observation_idx ON charge_components (account_id, observation_id);
ALTER TABLE charge_components ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON charge_components USING (account_id = current_account_id());
REVOKE UPDATE ON charge_components FROM lastro_app;

-- Components were kept inside the statement line observation until now; every
-- statement read so far is in BRL, whose amounts have two decimal places.
INSERT INTO charge_components (id, account_id, observation_id, statement_id, kind, amount_minor, currency, included)
SELECT gen_random_uuid(), o.account_id, o.id, (o.payload->>'statement_id')::uuid, c->>'kind',
       round((c->>'amount')::numeric * 100)::bigint, o.payload->>'currency', (c->>'included')::boolean
FROM observations o
CROSS JOIN LATERAL jsonb_array_elements(o.payload->'components') AS c
WHERE o.source = 'statement_line'
  AND jsonb_typeof(o.payload->'components') = 'array'
  AND o.payload->>'currency' = 'BRL'
ON CONFLICT DO NOTHING;

-- +goose Down
DROP TABLE charge_components;
