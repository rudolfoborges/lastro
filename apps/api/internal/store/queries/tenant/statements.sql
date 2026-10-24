-- name: InsertStatement :one
INSERT INTO statements (id, account_id, issuer, issuer_key, card_last4, closing_date, payment_method_id)
VALUES (@id, @account_id, @issuer, @issuer_key, @card_last4, @closing_date, @payment_method_id)
ON CONFLICT (account_id, issuer_key, card_last4, closing_date) DO NOTHING
RETURNING *;

-- name: LockStatementByIdentity :one
SELECT *
FROM statements
WHERE account_id = @account_id AND issuer_key = @issuer_key AND card_last4 = @card_last4 AND closing_date = @closing_date
FOR UPDATE;

-- name: GetStatement :one
SELECT *
FROM statements
WHERE account_id = @account_id AND id = @id;

-- name: InsertStatementVersion :one
INSERT INTO statement_versions (
    id, account_id, statement_id, version, issue_date, due_date, period_start, period_end, currency,
    informed_total_minor, previous_balance_minor, reading, missing, page_count, file_sha256, content_hash
)
VALUES (
    sqlc.arg(id), sqlc.arg(account_id), sqlc.arg(statement_id), sqlc.arg(statement_version), sqlc.narg(issue_date), sqlc.arg(due_date), sqlc.narg(period_start), sqlc.narg(period_end), sqlc.arg(currency),
    sqlc.arg(informed_total_minor), sqlc.narg(previous_balance_minor), sqlc.arg(reading), sqlc.narg(missing), sqlc.narg(page_count), sqlc.narg(file_sha256), sqlc.arg(content_hash)
)
RETURNING *;

-- name: LatestStatementVersion :one
SELECT *
FROM statement_versions
WHERE account_id = @account_id AND statement_id = @statement_id
ORDER BY version DESC
LIMIT 1;

-- name: InsertStatementLine :exec
INSERT INTO statement_lines (id, account_id, statement_version_id, observation_id, position, kind, direction, amount_minor, page, excerpt, details)
VALUES (@id, @account_id, @statement_version_id, @observation_id, @position, @kind, @direction, @amount_minor, @page, @excerpt, @details);

-- name: ListStatementLines :many
SELECT l.*, e.transaction_id
FROM statement_lines l
LEFT JOIN transaction_evidence e ON e.account_id = l.account_id AND e.observation_id = l.observation_id AND e.status = 'confirmed'
WHERE l.account_id = @account_id AND l.statement_version_id = @statement_version_id
ORDER BY l.position;

-- name: GetObservationBySourceKey :one
SELECT *
FROM observations
WHERE account_id = @account_id AND source = @source AND source_key = @source_key;

-- name: FindStatementMatchCandidates :many
SELECT t.*
FROM transactions t
LEFT JOIN payment_methods pm ON pm.account_id = t.account_id AND pm.id = t.payment_method_id
WHERE t.account_id = @account_id
  AND t.status NOT IN ('voided', 'merged')
  AND t.nature = @nature
  AND (
      (t.amount_minor = @amount_minor AND t.currency = @currency)
      OR (t.amount_minor = sqlc.narg(original_amount_minor) AND t.currency = sqlc.narg(original_currency))
  )
  AND (pm.id IS NULL OR pm.kind = 'credit_card')
  AND (t.occurred_on BETWEEN @from_day::date AND @to_day::date
       OR (t.occurred_on IS NULL AND t.created_at BETWEEN @from_at::timestamptz AND @to_at::timestamptz))
  AND NOT EXISTS (
      SELECT FROM transaction_evidence e
      JOIN observations o ON o.account_id = e.account_id AND o.id = e.observation_id
      WHERE e.account_id = t.account_id AND e.transaction_id = t.id AND e.status = 'confirmed' AND o.source = 'statement_line'
  )
ORDER BY t.occurred_on NULLS LAST, t.created_at, t.id
LIMIT 5;

-- name: GetStatementVersion :one
SELECT *
FROM statement_versions
WHERE account_id = @account_id AND id = @id;
