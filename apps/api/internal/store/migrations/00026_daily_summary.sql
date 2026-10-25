-- +goose Up
ALTER TABLE accounts
    ADD COLUMN daily_summary_enabled boolean NOT NULL DEFAULT false,
    ADD COLUMN daily_summary_time text NOT NULL DEFAULT '08:00' CHECK (daily_summary_time ~ '^([01][0-9]|2[0-3]):[0-5][0-9]$');

CREATE TABLE daily_summary_deliveries (
    account_id   uuid NOT NULL REFERENCES accounts (id),
    day          date NOT NULL,
    delivered_at timestamptz NOT NULL,
    delivered_by text NOT NULL CHECK (char_length(delivered_by) BETWEEN 1 AND 200),
    PRIMARY KEY (account_id, day)
);
ALTER TABLE daily_summary_deliveries ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON daily_summary_deliveries USING (account_id = current_account_id());

-- +goose Down
DROP TABLE daily_summary_deliveries;
ALTER TABLE accounts DROP COLUMN daily_summary_time, DROP COLUMN daily_summary_enabled;
