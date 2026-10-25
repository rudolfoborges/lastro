-- name: GetDailySummaryDelivery :one
SELECT *
FROM daily_summary_deliveries
WHERE account_id = @account_id AND day = @day;

-- name: InsertDailySummaryDelivery :one
INSERT INTO daily_summary_deliveries (account_id, day, delivered_at, delivered_by)
VALUES (@account_id, @day, @delivered_at, @delivered_by)
ON CONFLICT (account_id, day) DO NOTHING
RETURNING *;
