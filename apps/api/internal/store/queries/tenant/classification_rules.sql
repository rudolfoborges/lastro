-- name: InsertClassificationRule :one
INSERT INTO classification_rules (
    id, account_id, merchant, merchant_key, category_id, purpose, instruction, status,
    min_amount_minor, max_amount_minor, currency, card_last4, payment_method, conditions_key,
    rationale, based_on, approved_at
)
VALUES (
    @id, @account_id, @merchant, @merchant_key, @category_id, sqlc.narg(purpose), sqlc.narg(instruction), @status,
    sqlc.narg(min_amount_minor), sqlc.narg(max_amount_minor), sqlc.narg(currency), sqlc.narg(card_last4), sqlc.narg(payment_method), @conditions_key,
    sqlc.narg(rationale), sqlc.narg(based_on)::uuid[], sqlc.narg(approved_at)
)
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
WHERE r.account_id = @account_id AND (r.status IN ('active', 'suggested') OR @include_archived::boolean)
ORDER BY r.status, r.merchant_key, r.conditions_key, r.created_at;

-- name: ArchiveClassificationRule :exec
UPDATE classification_rules
SET status = 'archived', archived_at = now(), archived_reason = @archived_reason
WHERE account_id = @account_id AND id = @id AND status IN ('active', 'suggested');

-- name: ApproveClassificationRule :exec
UPDATE classification_rules
SET status = 'active', instruction = @instruction, approved_at = @approved_at
WHERE account_id = @account_id AND id = @id AND status = 'suggested';
