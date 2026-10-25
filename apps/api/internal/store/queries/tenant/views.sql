-- name: SpendingRows :many
SELECT t.id, t.description, t.merchant, t.amount_minor, t.currency, t.amount_basis, t.occurred_on,
       t.nature, t.status, t.refund_of_id, t.installment_count, t.installment_amount_minor,
       pm.kind AS payment_kind, pm.card_last4,
       c.code AS category_code, c.label AS category_label, a.status AS category_status,
       p.payment_modality,
       ARRAY(
           SELECT DISTINCT o.source
           FROM transaction_evidence e
           JOIN observations o ON o.account_id = e.account_id AND o.id = e.observation_id
           WHERE e.account_id = t.account_id AND e.transaction_id = t.id AND e.status = 'confirmed'
       )::text[] AS sources
FROM transactions t
LEFT JOIN payment_methods pm ON pm.account_id = t.account_id AND pm.id = t.payment_method_id
LEFT JOIN category_allocations a ON a.account_id = t.account_id AND a.transaction_id = t.id AND a.superseded_at IS NULL
LEFT JOIN categories c ON c.account_id = a.account_id AND c.id = a.category_id
LEFT JOIN installment_plans p ON p.account_id = t.account_id AND p.transaction_id = t.id AND p.canceled_at IS NULL
WHERE t.account_id = @account_id
  AND t.status IN ('provisional', 'confirmed', 'pending_clarification')
  AND t.nature IN ('expense', 'refund')
  AND t.occurred_on BETWEEN @from_day::date AND @to_day::date
  AND (sqlc.narg(statement_id)::uuid IS NULL OR EXISTS (
      SELECT FROM transaction_evidence e
      JOIN statement_lines l ON l.account_id = e.account_id AND l.observation_id = e.observation_id
      JOIN statement_versions v ON v.account_id = l.account_id AND v.id = l.statement_version_id
      WHERE e.account_id = t.account_id AND e.transaction_id = t.id AND e.status = 'confirmed'
        AND v.statement_id = sqlc.narg(statement_id)::uuid
  ))
ORDER BY t.occurred_on, t.created_at, t.id;

-- name: CountUndatedPurchases :one
SELECT count(*)::int
FROM transactions
WHERE account_id = @account_id
  AND status IN ('provisional', 'confirmed', 'pending_clarification')
  AND nature IN ('expense', 'refund')
  AND occurred_on IS NULL;

-- name: StatementLineRows :many
SELECT l.position, l.kind, l.direction, l.amount_minor,
       e.transaction_id,
       c.code AS category_code, c.label AS category_label,
       p.payment_modality,
       coalesce((
           SELECT sum(cc.amount_minor)
           FROM charge_components cc
           WHERE cc.account_id = l.account_id AND cc.observation_id = l.observation_id AND cc.included AND cc.kind <> 'principal'
       ), 0)::bigint AS included_charges_minor
FROM statement_lines l
LEFT JOIN transaction_evidence e ON e.account_id = l.account_id AND e.observation_id = l.observation_id AND e.status = 'confirmed'
LEFT JOIN category_allocations a ON a.account_id = l.account_id AND a.transaction_id = e.transaction_id AND a.superseded_at IS NULL
LEFT JOIN categories c ON c.account_id = a.account_id AND c.id = a.category_id
LEFT JOIN installment_plans p ON p.account_id = l.account_id AND p.transaction_id = e.transaction_id AND p.canceled_at IS NULL
WHERE l.account_id = @account_id AND l.statement_version_id = @statement_version_id
ORDER BY l.position;

-- name: ActivePlanTransactions :many
SELECT p.transaction_id
FROM installment_plans p
JOIN transactions t ON t.account_id = p.account_id AND t.id = p.transaction_id
WHERE p.account_id = @account_id AND p.canceled_at IS NULL AND t.status NOT IN ('voided', 'merged')
ORDER BY t.occurred_on NULLS LAST, t.created_at, t.id;

-- name: ListStatementsWithLatestVersion :many
SELECT s.*, v.id AS version_id
FROM statements s
JOIN LATERAL (
    SELECT sv.id
    FROM statement_versions sv
    WHERE sv.account_id = s.account_id AND sv.statement_id = s.id
    ORDER BY sv.version DESC
    LIMIT 1
) v ON true
WHERE s.account_id = @account_id
ORDER BY s.closing_date, s.card_last4;
