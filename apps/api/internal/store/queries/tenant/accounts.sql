-- name: GetAccount :one
SELECT *
FROM accounts
WHERE id = @id;
