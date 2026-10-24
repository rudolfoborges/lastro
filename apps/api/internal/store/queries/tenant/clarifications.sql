-- name: InsertClarification :exec
INSERT INTO clarifications (id, account_id, kind, transaction_id, observation_id, statement_id, subject, dedup_key, question, context, status)
VALUES (@id, @account_id, @kind, sqlc.narg(transaction_id), sqlc.narg(observation_id), sqlc.narg(statement_id), @subject, @dedup_key, @question, @context, 'open')
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
  AND (
      sqlc.narg(statement_id)::uuid IS NULL
      OR c.statement_id = sqlc.narg(statement_id)::uuid
      OR c.transaction_id IN (
          SELECT e.transaction_id
          FROM statement_lines l
          JOIN statement_versions v ON v.account_id = l.account_id AND v.id = l.statement_version_id
          JOIN transaction_evidence e ON e.account_id = l.account_id AND e.observation_id = l.observation_id AND e.status = 'confirmed'
          WHERE l.account_id = c.account_id AND v.statement_id = sqlc.narg(statement_id)::uuid
      )
  )
ORDER BY t.occurred_on NULLS LAST, c.created_at, c.id
LIMIT @max_rows;

-- name: WasClarificationDismissed :one
SELECT EXISTS (
    SELECT FROM clarifications
    WHERE account_id = @account_id AND dedup_key = @dedup_key AND status = 'dismissed'
);
