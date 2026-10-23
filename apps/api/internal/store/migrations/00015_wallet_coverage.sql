-- +goose Up
CREATE TABLE wallet_tracking_periods (
    id                     uuid PRIMARY KEY,
    account_id             uuid NOT NULL REFERENCES accounts (id),
    started_at             timestamptz NOT NULL,
    ended_at               timestamptz,
    inbox_interval_minutes integer NOT NULL CHECK (inbox_interval_minutes BETWEEN 15 AND 10080),
    UNIQUE (account_id, id),
    CHECK (ended_at IS NULL OR ended_at >= started_at)
);
CREATE UNIQUE INDEX wallet_tracking_periods_one_open_idx ON wallet_tracking_periods (account_id) WHERE ended_at IS NULL;
ALTER TABLE wallet_tracking_periods ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON wallet_tracking_periods USING (account_id = current_account_id());

CREATE TABLE wallet_heartbeats (
    id                   uuid PRIMARY KEY,
    account_id           uuid NOT NULL REFERENCES accounts (id),
    wallet_device_id     uuid NOT NULL,
    received_at          timestamptz NOT NULL,
    sent_at              timestamptz NOT NULL,
    client_version       text NOT NULL CHECK (client_version ~ '^[A-Za-z0-9._+-]{1,50}$'),
    notification_access  boolean NOT NULL,
    battery_unrestricted boolean,
    queue_size           integer CHECK (queue_size >= 0),
    FOREIGN KEY (account_id, wallet_device_id) REFERENCES wallet_devices (account_id, id)
);
CREATE INDEX wallet_heartbeats_account_received_idx ON wallet_heartbeats (account_id, received_at);
ALTER TABLE wallet_heartbeats ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON wallet_heartbeats USING (account_id = current_account_id());
REVOKE UPDATE ON wallet_heartbeats FROM lastro_app;

-- +goose Down
DROP TABLE wallet_heartbeats;
DROP TABLE wallet_tracking_periods;
