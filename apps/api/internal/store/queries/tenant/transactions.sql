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
