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
