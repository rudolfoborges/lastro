-- name: GetAccount :one
SELECT *
FROM accounts
WHERE id = @id;

-- name: LockAccount :one
SELECT id
FROM accounts
WHERE id = @id
FOR UPDATE;
