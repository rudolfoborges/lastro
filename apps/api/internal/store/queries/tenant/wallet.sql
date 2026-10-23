-- name: UpsertWalletDevice :one
INSERT INTO wallet_devices (id, account_id, device_id, last_received_at)
VALUES (@id, @account_id, @device_id, @received_at)
ON CONFLICT (account_id, device_id) DO UPDATE
SET last_received_at = greatest(wallet_devices.last_received_at, excluded.last_received_at)
RETURNING *;

-- name: CountDiscardedNotification :exec
UPDATE wallet_devices
SET discarded_count = discarded_count + 1
WHERE account_id = @account_id AND id = @id;

-- name: UpsertWalletNotification :one
INSERT INTO wallet_notifications (id, account_id, wallet_device_id, notification_key, package)
VALUES (@id, @account_id, @wallet_device_id, @notification_key, @package)
ON CONFLICT (account_id, wallet_device_id, notification_key) DO UPDATE
SET notification_key = excluded.notification_key
RETURNING *;

-- name: LockWalletNotificationByKey :one
SELECT *
FROM wallet_notifications
WHERE account_id = @account_id AND wallet_device_id = @wallet_device_id AND notification_key = @notification_key
FOR UPDATE;

-- name: MarkWalletNotificationRemoved :one
UPDATE wallet_notifications
SET removed_at = @removed_at
WHERE account_id = @account_id AND id = @id
RETURNING *;

-- name: FindWalletNotificationVersion :one
SELECT *
FROM wallet_notification_versions
WHERE account_id = @account_id AND notification_id = @notification_id AND content_hash = @content_hash;

-- name: InsertWalletNotificationVersion :one
INSERT INTO wallet_notification_versions (account_id, notification_id, version, observation_id, content_hash, event, posted_at, received_at)
VALUES (
    @account_id, @notification_id,
    (SELECT coalesce(max(version), 0) + 1 FROM wallet_notification_versions WHERE notification_id = @notification_id),
    @observation_id, @content_hash, @event, @posted_at, @received_at
)
RETURNING *;

