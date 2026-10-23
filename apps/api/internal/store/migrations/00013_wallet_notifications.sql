-- +goose Up
CREATE TABLE wallet_devices (
    id               uuid PRIMARY KEY,
    account_id       uuid NOT NULL REFERENCES accounts (id),
    device_id        text NOT NULL CHECK (device_id ~ '^[A-Za-z0-9._:-]{1,100}$'),
    discarded_count  bigint NOT NULL DEFAULT 0 CHECK (discarded_count >= 0),
    last_received_at timestamptz NOT NULL,
    created_at       timestamptz NOT NULL DEFAULT now(),
    UNIQUE (account_id, id),
    UNIQUE (account_id, device_id)
);
ALTER TABLE wallet_devices ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON wallet_devices USING (account_id = current_account_id());

CREATE TABLE wallet_notifications (
    id               uuid PRIMARY KEY,
    account_id       uuid NOT NULL REFERENCES accounts (id),
    wallet_device_id uuid NOT NULL,
    notification_key text NOT NULL CHECK (char_length(notification_key) BETWEEN 1 AND 300),
    package          text NOT NULL CHECK (char_length(package) BETWEEN 1 AND 255),
    removed_at       timestamptz,
    created_at       timestamptz NOT NULL DEFAULT now(),
    UNIQUE (account_id, id),
    UNIQUE (account_id, wallet_device_id, notification_key),
    FOREIGN KEY (account_id, wallet_device_id) REFERENCES wallet_devices (account_id, id)
);
ALTER TABLE wallet_notifications ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON wallet_notifications USING (account_id = current_account_id());

CREATE TABLE wallet_notification_versions (
    account_id      uuid NOT NULL REFERENCES accounts (id),
    notification_id uuid NOT NULL,
    version         integer NOT NULL CHECK (version > 0),
    observation_id  uuid NOT NULL UNIQUE,
    content_hash    bytea NOT NULL CHECK (octet_length(content_hash) = 32),
    event           text NOT NULL CHECK (event IN ('posted', 'updated')),
    posted_at       timestamptz NOT NULL,
    received_at     timestamptz NOT NULL,
    PRIMARY KEY (notification_id, version),
    UNIQUE (notification_id, content_hash),
    FOREIGN KEY (account_id, notification_id) REFERENCES wallet_notifications (account_id, id),
    FOREIGN KEY (account_id, observation_id) REFERENCES observations (account_id, id)
);
CREATE INDEX wallet_notification_versions_account_received_idx ON wallet_notification_versions (account_id, received_at);
ALTER TABLE wallet_notification_versions ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON wallet_notification_versions USING (account_id = current_account_id());
REVOKE UPDATE ON wallet_notification_versions FROM lastro_app;

-- +goose Down
DROP TABLE wallet_notification_versions;
DROP TABLE wallet_notifications;
DROP TABLE wallet_devices;
