-- name: InsertClarification :exec
INSERT INTO clarifications (id, account_id, kind, transaction_id, observation_id, subject, dedup_key, question, context, status)
VALUES (@id, @account_id, @kind, sqlc.narg(transaction_id), sqlc.narg(observation_id), @subject, @dedup_key, @question, @context, 'open')
ON CONFLICT (account_id, dedup_key) WHERE status = 'open' DO NOTHING;

-- name: GetOpenClarificationByDedupKey :one
SELECT *
FROM clarifications
WHERE account_id = @account_id AND dedup_key = @dedup_key AND status = 'open';

-- name: LockClarification :one
SELECT *
FROM clarifications
WHERE account_id = @account_id AND id = @id
FOR UPDATE;

-- name: CloseClarification :one
UPDATE clarifications
SET status = @status, resolution = @resolution, resolved_by = @resolved_by, resolved_at = now(), updated_at = now()
WHERE account_id = @account_id AND id = @id AND status = 'open'
RETURNING *;

-- name: OpenTransactionClarifications :many
SELECT *
FROM clarifications
WHERE account_id = @account_id AND transaction_id = @transaction_id AND status = 'open'
ORDER BY created_at, id;

-- name: ListClarifications :many
SELECT c.*
FROM clarifications c
LEFT JOIN transactions t ON t.account_id = c.account_id AND t.id = c.transaction_id
WHERE c.account_id = @account_id
  AND c.status = @status
  AND (cardinality(@kinds::text[]) = 0 OR c.kind = ANY (@kinds::text[]))
  AND (sqlc.narg(from_date)::date IS NULL OR t.occurred_on >= sqlc.narg(from_date)::date)
  AND (sqlc.narg(to_date)::date IS NULL OR t.occurred_on <= sqlc.narg(to_date)::date)
ORDER BY t.occurred_on NULLS LAST, c.created_at, c.id
LIMIT @max_rows;

-- name: WasClarificationDismissed :one
SELECT EXISTS (
    SELECT FROM clarifications
    WHERE account_id = @account_id AND dedup_key = @dedup_key AND status = 'dismissed'
);
