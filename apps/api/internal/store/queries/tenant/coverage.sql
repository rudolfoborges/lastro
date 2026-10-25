-- name: OpenTrackingPeriod :one
SELECT *
FROM wallet_tracking_periods
WHERE account_id = @account_id AND ended_at IS NULL
FOR UPDATE;

-- name: StartTrackingPeriod :one
INSERT INTO wallet_tracking_periods (id, account_id, started_at, inbox_interval_minutes)
VALUES (@id, @account_id, @started_at, @inbox_interval_minutes)
RETURNING *;

-- name: EndTrackingPeriod :one
UPDATE wallet_tracking_periods
SET ended_at = @ended_at
WHERE account_id = @account_id AND id = @id
RETURNING *;

-- name: SetInboxInterval :one
UPDATE wallet_tracking_periods
SET inbox_interval_minutes = @inbox_interval_minutes
WHERE account_id = @account_id AND id = @id
RETURNING *;

-- name: TrackingPeriodsBetween :many
SELECT *
FROM wallet_tracking_periods
WHERE account_id = @account_id
  AND started_at < @until
  AND (ended_at IS NULL OR ended_at > @since::timestamptz)
ORDER BY started_at;

-- name: InsertHeartbeat :one
INSERT INTO wallet_heartbeats (id, account_id, wallet_device_id, received_at, sent_at, client_version, notification_access, battery_unrestricted, queue_size)
VALUES (@id, @account_id, @wallet_device_id, @received_at, @sent_at, @client_version, @notification_access, sqlc.narg(battery_unrestricted), sqlc.narg(queue_size))
RETURNING *;

-- name: HeartbeatTimesBetween :many
SELECT received_at
FROM wallet_heartbeats
WHERE account_id = @account_id AND received_at >= @since AND received_at < @until
ORDER BY received_at;

-- name: LastHeartbeatBefore :one
SELECT received_at
FROM wallet_heartbeats
WHERE account_id = @account_id AND received_at < @until
ORDER BY received_at DESC
LIMIT 1;

-- name: ListWalletDevices :many
SELECT *
FROM wallet_devices
WHERE account_id = @account_id
ORDER BY device_id;

-- name: LatestHeartbeats :many
SELECT DISTINCT ON (wallet_device_id) *
FROM wallet_heartbeats
WHERE account_id = @account_id
ORDER BY wallet_device_id, received_at DESC;

-- name: LateDeliveries :one
SELECT count(*) AS late,
       coalesce(max(extract(epoch FROM received_at - posted_at)), 0)::bigint AS worst_seconds
FROM wallet_notification_versions
WHERE account_id = @account_id
  AND received_at >= @since AND received_at < @until
  AND extract(epoch FROM received_at - posted_at) > @threshold_seconds::bigint;

-- name: OldestPendingNotification :one
SELECT v.received_at
FROM wallet_notification_versions v
WHERE v.account_id = @account_id
  AND NOT EXISTS (
      SELECT FROM observation_interpretations i
      WHERE i.account_id = v.account_id AND i.observation_id = v.observation_id
  )
ORDER BY v.received_at
LIMIT 1;

-- name: LastToolCall :one
SELECT occurred_at
FROM tool_calls
WHERE account_id = @account_id AND tool = @tool AND outcome = 'completed'
ORDER BY occurred_at DESC
LIMIT 1;

-- name: IsTrackingActive :one
SELECT EXISTS (
    SELECT FROM wallet_tracking_periods
    WHERE account_id = @account_id AND ended_at IS NULL
);

-- name: LatestTrackingPeriod :one
SELECT *
FROM wallet_tracking_periods
WHERE account_id = @account_id
ORDER BY started_at DESC
LIMIT 1;

-- name: FirstTrackingStart :one
SELECT started_at
FROM wallet_tracking_periods
WHERE account_id = @account_id
ORDER BY started_at
LIMIT 1;
