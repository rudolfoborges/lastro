-- name: InsertClassificationRule :one
INSERT INTO classification_rules (id, account_id, merchant, merchant_key, category_id, purpose, instruction)
VALUES (@id, @account_id, @merchant, @merchant_key, @category_id, sqlc.narg(purpose), @instruction)
RETURNING *;

-- name: GetClassificationRule :one
SELECT r.*, c.code AS category_code, c.label AS category_label
FROM classification_rules r
JOIN categories c ON c.account_id = r.account_id AND c.id = r.category_id
WHERE r.account_id = @account_id AND r.id = @id;

-- name: ListClassificationRules :many
SELECT r.*, c.code AS category_code, c.label AS category_label
FROM classification_rules r
JOIN categories c ON c.account_id = r.account_id AND c.id = r.category_id
WHERE r.account_id = @account_id AND (r.status = 'active' OR @include_archived::boolean)
ORDER BY r.status, r.merchant_key, r.created_at;

-- name: ArchiveClassificationRule :exec
UPDATE classification_rules
SET status = 'archived', archived_at = now(), archived_reason = @archived_reason
WHERE account_id = @account_id AND id = @id AND status = 'active';
