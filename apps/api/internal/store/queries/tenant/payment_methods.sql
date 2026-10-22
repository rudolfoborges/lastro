-- name: InsertPaymentMethod :exec
INSERT INTO payment_methods (id, account_id, kind, card_last4)
VALUES (@id, @account_id, @kind, sqlc.narg(card_last4))
ON CONFLICT DO NOTHING;

-- name: FindPaymentMethod :one
SELECT *
FROM payment_methods
WHERE account_id = @account_id
  AND kind = @kind
  AND card_last4 IS NOT DISTINCT FROM sqlc.narg(card_last4);

-- name: GetPaymentMethod :one
SELECT *
FROM payment_methods
WHERE account_id = @account_id AND id = @id;
