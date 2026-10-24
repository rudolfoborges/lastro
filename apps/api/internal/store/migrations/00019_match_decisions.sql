-- +goose Up
CREATE TABLE match_rejections (
    account_id uuid NOT NULL REFERENCES accounts (id),
    low_id     uuid NOT NULL,
    high_id    uuid NOT NULL,
    reason     text NOT NULL CHECK (char_length(btrim(reason)) BETWEEN 1 AND 500),
    author     text NOT NULL CHECK (author IN ('user', 'agent', 'system')),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (account_id, low_id, high_id),
    CHECK (low_id < high_id),
    FOREIGN KEY (account_id, low_id) REFERENCES transactions (account_id, id),
    FOREIGN KEY (account_id, high_id) REFERENCES transactions (account_id, id)
);
ALTER TABLE match_rejections ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON match_rejections USING (account_id = current_account_id());

CREATE TABLE transaction_merges (
    id              uuid PRIMARY KEY,
    account_id      uuid NOT NULL REFERENCES accounts (id),
    survivor_id     uuid NOT NULL,
    merged_id       uuid NOT NULL,
    merged_status   text NOT NULL CHECK (merged_status IN ('provisional', 'confirmed', 'pending_clarification')),
    observation_ids uuid[] NOT NULL,
    refund_ids      uuid[] NOT NULL DEFAULT '{}',
    changes         jsonb NOT NULL DEFAULT '[]' CHECK (jsonb_typeof(changes) = 'array'),
    reason          text NOT NULL CHECK (char_length(btrim(reason)) BETWEEN 1 AND 500),
    author          text NOT NULL CHECK (author IN ('user', 'agent', 'system')),
    created_at      timestamptz NOT NULL DEFAULT now(),
    undone_at       timestamptz,
    undo_reason     text CHECK (char_length(btrim(undo_reason)) BETWEEN 1 AND 500),
    UNIQUE (account_id, id),
    FOREIGN KEY (account_id, survivor_id) REFERENCES transactions (account_id, id),
    FOREIGN KEY (account_id, merged_id) REFERENCES transactions (account_id, id),
    CHECK (survivor_id <> merged_id),
    CHECK ((undone_at IS NULL) = (undo_reason IS NULL))
);
CREATE UNIQUE INDEX transaction_merges_active_idx ON transaction_merges (merged_id) WHERE undone_at IS NULL;
CREATE INDEX transaction_merges_survivor_idx ON transaction_merges (account_id, survivor_id) WHERE undone_at IS NULL;
ALTER TABLE transaction_merges ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON transaction_merges USING (account_id = current_account_id());

-- +goose Down
DROP TABLE transaction_merges;
DROP TABLE match_rejections;
