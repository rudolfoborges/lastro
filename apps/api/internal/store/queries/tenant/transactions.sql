-- name: InsertTransaction :one
INSERT INTO transactions (
    id, account_id, original_description, description, merchant, payee, amount_minor, currency,
    original_amount_minor, original_currency, occurred_at, occurred_on, date_precision, date_source,
    payment_method_id, nature, status, purpose, installment_count, installment_amount_minor, refund_of_id
)
VALUES (
    @id, @account_id, @original_description, @description, sqlc.narg(merchant), sqlc.narg(payee), @amount_minor, @currency,
    sqlc.narg(original_amount_minor), sqlc.narg(original_currency), sqlc.narg(occurred_at), sqlc.narg(occurred_on),
    @date_precision, @date_source, sqlc.narg(payment_method_id), @nature, @status, sqlc.narg(purpose),
    sqlc.narg(installment_count), sqlc.narg(installment_amount_minor), sqlc.narg(refund_of_id)
)
RETURNING *;

-- name: GetTransaction :one
SELECT *
FROM transactions
WHERE account_id = @account_id AND id = @id;

-- name: LockTransaction :one
SELECT *
FROM transactions
WHERE account_id = @account_id AND id = @id
FOR UPDATE;

-- name: UpdateTransaction :one
UPDATE transactions
SET description = @description,
    merchant = sqlc.narg(merchant),
    payee = sqlc.narg(payee),
    amount_minor = @amount_minor,
    currency = @currency,
    original_amount_minor = sqlc.narg(original_amount_minor),
    original_currency = sqlc.narg(original_currency),
    occurred_at = sqlc.narg(occurred_at),
    occurred_on = sqlc.narg(occurred_on),
    date_precision = @date_precision,
    date_source = @date_source,
    payment_method_id = sqlc.narg(payment_method_id),
    nature = @nature,
    status = @status,
    purpose = sqlc.narg(purpose),
    installment_count = sqlc.narg(installment_count),
    installment_amount_minor = sqlc.narg(installment_amount_minor),
    updated_at = now()
WHERE account_id = @account_id AND id = @id
RETURNING *;

-- name: HasConfirmedEvidenceFrom :one
SELECT EXISTS (
    SELECT
    FROM transaction_evidence e
    JOIN observations o ON o.account_id = e.account_id AND o.id = e.observation_id
    WHERE e.account_id = @account_id
      AND e.transaction_id = @transaction_id
      AND e.status = 'confirmed'
      AND o.source = @source
);

-- name: ListTransactions :many
SELECT t.*
FROM transactions t
LEFT JOIN payment_methods pm ON pm.account_id = t.account_id AND pm.id = t.payment_method_id
WHERE t.account_id = @account_id
  AND t.status = ANY (@statuses::text[])
  AND (sqlc.narg(from_date)::date IS NULL OR t.occurred_on >= sqlc.narg(from_date)::date)
  AND (sqlc.narg(to_date)::date IS NULL OR t.occurred_on <= sqlc.narg(to_date)::date)
  AND (sqlc.narg(payment_kind)::text IS NULL OR pm.kind = sqlc.narg(payment_kind)::text)
  AND (
      sqlc.narg(category_code)::text IS NULL
      OR EXISTS (
          SELECT
          FROM category_allocations a
          JOIN categories c ON c.account_id = a.account_id AND c.id = a.category_id
          WHERE a.account_id = t.account_id AND a.transaction_id = t.id AND a.superseded_at IS NULL
            AND c.code = sqlc.narg(category_code)::text
      )
      OR (
          sqlc.narg(category_code)::text = 'unidentified'
          AND NOT EXISTS (
              SELECT FROM category_allocations a
              WHERE a.account_id = t.account_id AND a.transaction_id = t.id AND a.superseded_at IS NULL
          )
      )
  )
ORDER BY t.occurred_on DESC NULLS LAST, t.created_at DESC, t.id DESC
LIMIT @max_rows OFFSET @skip_rows;

-- name: ListPaymentMethods :many
SELECT *
FROM payment_methods
WHERE account_id = @account_id;
