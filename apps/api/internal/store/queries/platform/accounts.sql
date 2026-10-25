-- name: CreateAccount :one
INSERT INTO accounts (id, name)
VALUES (@id, @name)
RETURNING *;

-- name: ListAccounts :many
SELECT *
FROM accounts
WHERE sandbox_of IS NULL
ORDER BY created_at, id;
