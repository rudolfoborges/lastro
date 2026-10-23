-- +goose Up
ALTER TABLE observation_interpretations
    DROP CONSTRAINT observation_interpretations_event_type_check,
    ADD CONSTRAINT observation_interpretations_event_type_check CHECK (event_type IN (
        'purchase', 'purchase_update', 'cancellation', 'refund', 'declined', 'notice', 'unrelated', 'unknown'
    )),
    ADD COLUMN effect text CHECK (effect IN ('none', 'created', 'linked', 'updated', 'voided', 'refund_created', 'undone')),
    ADD COLUMN transaction_id uuid,
    ADD COLUMN submission_hash bytea CHECK (octet_length(submission_hash) = 32),
    ADD CONSTRAINT observation_interpretations_transaction_fkey
        FOREIGN KEY (account_id, transaction_id) REFERENCES transactions (account_id, id);

ALTER TABLE transactions
    ADD CONSTRAINT transactions_refund_nature_check CHECK (refund_of_id IS NULL OR nature = 'refund');

-- +goose Down
ALTER TABLE transactions
    DROP CONSTRAINT transactions_refund_nature_check;

ALTER TABLE observation_interpretations
    DROP CONSTRAINT observation_interpretations_transaction_fkey,
    DROP COLUMN submission_hash,
    DROP COLUMN transaction_id,
    DROP COLUMN effect,
    DROP CONSTRAINT observation_interpretations_event_type_check,
    ADD CONSTRAINT observation_interpretations_event_type_check CHECK (event_type IN (
        'purchase', 'purchase_update', 'cancellation', 'refund', 'declined', 'notice', 'unrelated'
    ));
