-- +goose Up
ALTER TABLE accounts
    ADD COLUMN statement_month text NOT NULL DEFAULT 'closing' CHECK (statement_month IN ('closing', 'due'));

-- +goose Down
ALTER TABLE accounts DROP COLUMN statement_month;
