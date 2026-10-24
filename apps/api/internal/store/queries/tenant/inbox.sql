-- name: ListPendingNotifications :many
SELECT v.observation_id, v.notification_id, v.version, v.event, v.posted_at, v.received_at,
       n.package, n.removed_at, d.device_id, o.payload
FROM wallet_notification_versions v
JOIN wallet_notifications n ON n.account_id = v.account_id AND n.id = v.notification_id
JOIN wallet_devices d ON d.account_id = n.account_id AND d.id = n.wallet_device_id
JOIN observations o ON o.account_id = v.account_id AND o.id = v.observation_id
WHERE v.account_id = @account_id
  AND NOT EXISTS (
      SELECT FROM observation_interpretations i
      WHERE i.account_id = v.account_id AND i.observation_id = v.observation_id
  )
  AND (sqlc.narg(after_received_at)::timestamptz IS NULL
       OR (v.received_at, v.observation_id) > (sqlc.narg(after_received_at)::timestamptz, sqlc.narg(after_observation_id)::uuid))
ORDER BY v.received_at, v.observation_id
LIMIT @row_limit;

-- name: CountPendingNotifications :one
SELECT count(*)
FROM wallet_notification_versions v
WHERE v.account_id = @account_id
  AND NOT EXISTS (
      SELECT FROM observation_interpretations i
      WHERE i.account_id = v.account_id AND i.observation_id = v.observation_id
  );

-- name: ListNotificationVersions :many
SELECT v.observation_id, v.notification_id, v.version, v.event, v.posted_at, v.received_at, o.payload
FROM wallet_notification_versions v
JOIN observations o ON o.account_id = v.account_id AND o.id = v.observation_id
WHERE v.account_id = @account_id AND v.notification_id = ANY(@notification_ids::uuid[])
ORDER BY v.notification_id, v.version;

-- name: LatestInterpretations :many
SELECT DISTINCT ON (observation_id) *
FROM observation_interpretations
WHERE account_id = @account_id AND observation_id = ANY(@observation_ids::uuid[])
ORDER BY observation_id, version DESC;

-- name: LockNotificationVersion :one
SELECT v.observation_id, v.notification_id, v.version, v.event, v.posted_at, v.received_at,
       n.package, n.removed_at, d.device_id, o.payload
FROM wallet_notification_versions v
JOIN wallet_notifications n ON n.account_id = v.account_id AND n.id = v.notification_id
JOIN wallet_devices d ON d.account_id = n.account_id AND d.id = n.wallet_device_id
JOIN observations o ON o.account_id = v.account_id AND o.id = v.observation_id
WHERE v.account_id = @account_id AND v.observation_id = @observation_id
FOR UPDATE OF n;

-- name: NotificationTransactionIDs :many
SELECT DISTINCT e.transaction_id
FROM wallet_notification_versions v
JOIN transaction_evidence e ON e.account_id = v.account_id AND e.observation_id = v.observation_id AND e.status = 'confirmed'
WHERE v.account_id = @account_id AND v.notification_id = @notification_id;

-- name: TransactionNotificationIDs :many
SELECT DISTINCT v.notification_id
FROM transaction_evidence e
JOIN wallet_notification_versions v ON v.account_id = e.account_id AND v.observation_id = e.observation_id
WHERE e.account_id = @account_id AND e.transaction_id = @transaction_id AND e.status = 'confirmed';

-- name: LockConfirmedEvidence :one
SELECT *
FROM transaction_evidence
WHERE account_id = @account_id AND transaction_id = @transaction_id AND observation_id = @observation_id AND status = 'confirmed'
FOR UPDATE;

-- name: CountConfirmedEvidence :one
SELECT count(*)
FROM transaction_evidence
WHERE account_id = @account_id AND transaction_id = @transaction_id AND status = 'confirmed';

-- name: SumRefunds :one
SELECT coalesce(sum(amount_minor), 0)::bigint
FROM transactions
WHERE account_id = @account_id AND refund_of_id = @refund_of_id AND status NOT IN ('voided', 'merged');
