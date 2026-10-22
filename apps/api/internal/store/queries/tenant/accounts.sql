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
