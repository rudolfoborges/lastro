-- name: CreateCategory :exec
INSERT INTO categories (id, account_id, code, label)
VALUES (@id, @account_id, @code, @label);
