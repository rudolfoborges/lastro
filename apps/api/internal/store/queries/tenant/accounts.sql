-- name: GetAccount :one
SELECT *
FROM accounts
WHERE id = @id;

-- name: LockAccount :one
SELECT id
FROM accounts
WHERE id = @id
FOR UPDATE;

-- name: UpdateAccountTimezone :one
UPDATE accounts
SET timezone = @timezone
WHERE id = @id
RETURNING *;

-- name: UpdateDailySummarySettings :one
UPDATE accounts
SET daily_summary_enabled = @enabled, daily_summary_time = @at
WHERE id = @id
RETURNING *;
