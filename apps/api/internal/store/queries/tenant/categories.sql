-- name: ListCategories :many
SELECT *
FROM categories
WHERE account_id = @account_id
ORDER BY archived_at IS NOT NULL, label, code;

-- name: GetCategoryByCode :one
SELECT *
FROM categories
WHERE account_id = @account_id AND code = @code;

-- name: InsertCategory :one
INSERT INTO categories (id, account_id, code, label)
VALUES (@id, @account_id, @code, @label)
RETURNING *;

-- name: UpdateCategory :one
UPDATE categories
SET label = @label, archived_at = sqlc.narg(archived_at)
WHERE account_id = @account_id AND code = @code
RETURNING *;

-- name: CurrentAllocations :many
SELECT a.id, a.category_id, c.code, c.label, c.archived_at, a.amount_minor, a.status, a.origin, a.created_at
FROM category_allocations a
JOIN categories c ON c.account_id = a.account_id AND c.id = a.category_id
WHERE a.account_id = @account_id AND a.transaction_id = @transaction_id AND a.superseded_at IS NULL
ORDER BY a.amount_minor DESC, c.code;

-- name: SupersedeAllocations :exec
UPDATE category_allocations
SET superseded_at = now()
WHERE account_id = @account_id AND transaction_id = @transaction_id AND superseded_at IS NULL;

-- name: InsertAllocation :exec
INSERT INTO category_allocations (id, account_id, transaction_id, category_id, amount_minor, status, origin)
VALUES (@id, @account_id, @transaction_id, @category_id, @amount_minor, @status, @origin);
