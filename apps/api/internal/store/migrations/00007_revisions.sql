-- +goose Up
CREATE TABLE revisions (
    id             uuid PRIMARY KEY,
    account_id     uuid NOT NULL REFERENCES accounts (id),
    entity_type    text NOT NULL CHECK (entity_type IN ('transaction', 'clarification')),
    entity_id      uuid NOT NULL,
    field          text NOT NULL CHECK (field ~ '^[a-z][a-z_]{0,49}$'),
    old_value      jsonb,
    new_value      jsonb,
    reason         text NOT NULL CHECK (char_length(btrim(reason)) BETWEEN 1 AND 500),
    author         text NOT NULL CHECK (author IN ('user', 'agent', 'rule', 'system')),
    actor_ref      text CHECK (char_length(actor_ref) BETWEEN 1 AND 100),
    observation_id uuid,
    created_at     timestamptz NOT NULL DEFAULT clock_timestamp(),
    FOREIGN KEY (account_id, observation_id) REFERENCES observations (account_id, id)
);
CREATE INDEX revisions_entity_idx ON revisions (account_id, entity_type, entity_id, created_at);
ALTER TABLE revisions ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON revisions USING (account_id = current_account_id());
REVOKE UPDATE ON revisions FROM lastro_app;

-- +goose Down
DROP TABLE revisions;
