-- name: CreateAccount :one
INSERT INTO accounts (id, name)
VALUES (@id, @name)
RETURNING *;

-- name: ListAccounts :many
SELECT *
FROM accounts
ORDER BY created_at, id;
