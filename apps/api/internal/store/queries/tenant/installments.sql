-- name: InsertInstallmentPlan :one
INSERT INTO installment_plans (
    id, account_id, transaction_id, payment_modality, installment_count, currency,
    installment_amount_minor, installment_amount_known, total_minor, card_last4, chain_key, informed_on
)
VALUES (
    @id, @account_id, @transaction_id, @payment_modality, @installment_count, @currency,
    sqlc.narg(installment_amount_minor), @installment_amount_known, sqlc.narg(total_minor), sqlc.narg(card_last4),
    sqlc.narg(chain_key), @informed_on
)
RETURNING *;

-- name: UpdateInstallmentPlan :one
UPDATE installment_plans
SET transaction_id = @transaction_id,
    payment_modality = @payment_modality,
    installment_count = @installment_count,
    installment_amount_minor = sqlc.narg(installment_amount_minor),
    installment_amount_known = @installment_amount_known,
    total_minor = sqlc.narg(total_minor),
    card_last4 = sqlc.narg(card_last4),
    chain_key = sqlc.narg(chain_key),
    informed_on = @informed_on,
    canceled_at = sqlc.narg(canceled_at),
    updated_at = now()
WHERE account_id = @account_id AND id = @id
RETURNING *;

-- name: PlanOfTransaction :one
SELECT *
FROM installment_plans
WHERE account_id = @account_id AND transaction_id = @transaction_id AND canceled_at IS NULL
FOR UPDATE;

-- name: GetInstallmentPlan :one
SELECT *
FROM installment_plans
WHERE account_id = @account_id AND id = @id
FOR UPDATE;

-- name: ChainablePlans :many
SELECT p.*
FROM installment_plans p
JOIN transactions t ON t.account_id = p.account_id AND t.id = p.transaction_id
WHERE p.account_id = @account_id
  AND p.canceled_at IS NULL
  AND t.status NOT IN ('voided', 'merged')
  AND p.chain_key = @chain_key
  AND p.installment_count = @installment_count
  AND p.card_last4 = @card_last4
  AND p.currency = @currency
  AND NOT EXISTS (
      SELECT FROM installments i
      WHERE i.account_id = p.account_id AND i.plan_id = p.id AND i.number >= @number AND i.state = 'charged'
  )
  AND EXISTS (
      SELECT FROM installments i
      WHERE i.account_id = p.account_id AND i.plan_id = p.id AND i.number = @previous_number AND i.state = 'charged'
        AND i.amount_minor = @amount_minor
  )
ORDER BY p.created_at, p.id;

-- name: FindInstallmentCandidates :many
SELECT t.*
FROM transactions t
LEFT JOIN payment_methods pm ON pm.account_id = t.account_id AND pm.id = t.payment_method_id
WHERE t.account_id = @account_id
  AND t.status NOT IN ('voided', 'merged')
  AND t.nature = 'expense'
  AND t.currency = @currency
  AND (pm.id IS NULL OR pm.kind = ANY(@payment_kinds::text[]))
  AND (t.installment_count = @installment_count
       OR t.amount_minor BETWEEN @low_total AND @high_total)
  AND (t.occurred_on BETWEEN @from_day::date AND @to_day::date
       OR (t.occurred_on IS NULL AND t.created_at BETWEEN @from_at::timestamptz AND @to_at::timestamptz))
  AND NOT EXISTS (
      SELECT FROM transaction_evidence e
      JOIN observations o ON o.account_id = e.account_id AND o.id = e.observation_id
      WHERE e.account_id = t.account_id AND e.transaction_id = t.id AND e.status = 'confirmed' AND o.source = 'statement_line'
  )
ORDER BY t.occurred_on NULLS LAST, t.created_at, t.id
LIMIT 5;

-- name: ListPlanInstallments :many
SELECT i.*, s.closing_date
FROM installments i
JOIN statements s ON s.account_id = i.account_id AND s.id = i.statement_id
WHERE i.account_id = @account_id AND i.plan_id = @plan_id
ORDER BY i.number;

-- name: UpsertChargedInstallment :one
INSERT INTO installments (id, account_id, plan_id, number, amount_minor, state, statement_id, observation_id, informed_on)
VALUES (@id, @account_id, @plan_id, @number, @amount_minor, 'charged', @statement_id, @observation_id, @informed_on)
ON CONFLICT (plan_id, number) DO UPDATE
SET amount_minor = excluded.amount_minor,
    state = 'charged',
    statement_id = excluded.statement_id,
    observation_id = excluded.observation_id,
    informed_on = excluded.informed_on,
    updated_at = now()
WHERE installments.state = 'known_future' OR installments.statement_id = excluded.statement_id
RETURNING *;

-- name: UpsertKnownInstallment :exec
INSERT INTO installments (id, account_id, plan_id, number, amount_minor, state, statement_id, informed_on)
VALUES (@id, @account_id, @plan_id, @number, @amount_minor, 'known_future', @statement_id, @informed_on)
ON CONFLICT (plan_id, number) DO UPDATE
SET amount_minor = excluded.amount_minor,
    statement_id = excluded.statement_id,
    informed_on = excluded.informed_on,
    updated_at = now()
WHERE installments.state = 'known_future';

-- name: MoveChargedInstallment :exec
UPDATE installments
SET observation_id = @to_observation_id, amount_minor = @amount_minor, updated_at = now()
WHERE account_id = @account_id AND observation_id = @from_observation_id;

-- name: MoveInstallmentsToPlan :exec
UPDATE installments
SET plan_id = @to_plan_id, updated_at = now()
WHERE account_id = @account_id AND plan_id = @from_plan_id;

-- name: CountChargedInstallments :one
SELECT count(*)
FROM installments
WHERE account_id = @account_id AND plan_id = @plan_id AND state = 'charged';

-- name: MoveInstallmentByObservation :exec
UPDATE installments
SET plan_id = @to_plan_id, updated_at = now()
WHERE account_id = @account_id AND observation_id = @observation_id;

-- name: InstallmentByObservation :one
SELECT *
FROM installments
WHERE account_id = @account_id AND observation_id = @observation_id;
