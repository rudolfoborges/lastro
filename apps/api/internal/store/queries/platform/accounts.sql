-- name: CreateAccount :one
INSERT INTO accounts (id, name)
VALUES (@id, @name)
RETURNING *;
