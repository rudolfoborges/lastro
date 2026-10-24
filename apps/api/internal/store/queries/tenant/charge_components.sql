-- name: InsertChargeComponent :exec
INSERT INTO charge_components (id, account_id, observation_id, statement_id, kind, amount_minor, currency, included)
VALUES (@id, @account_id, @observation_id, @statement_id, @kind, @amount_minor, @currency, @included)
ON CONFLICT (observation_id, kind) DO NOTHING;

-- name: PlanBreakdown :many
SELECT c.kind, sum(c.amount_minor)::bigint AS amount_minor, count(DISTINCT c.observation_id)::int AS installments
FROM charge_components c
JOIN installments i ON i.account_id = c.account_id AND i.observation_id = c.observation_id
WHERE c.account_id = @account_id AND i.plan_id = @plan_id AND i.state = 'charged' AND c.included
GROUP BY c.kind
ORDER BY c.kind;

-- name: ChargedWithoutBreakdown :one
SELECT count(*)::int
FROM installments i
WHERE i.account_id = @account_id AND i.plan_id = @plan_id AND i.state = 'charged'
  AND NOT EXISTS (
      SELECT FROM charge_components c
      WHERE c.account_id = i.account_id AND c.observation_id = i.observation_id AND c.included
  );

-- name: StatementIncludedCharges :one
SELECT coalesce(sum(c.amount_minor), 0)::bigint AS amount_minor, count(DISTINCT c.observation_id)::int AS lines
FROM charge_components c
JOIN statement_lines l ON l.account_id = c.account_id AND l.observation_id = c.observation_id
WHERE c.account_id = @account_id AND l.statement_version_id = @statement_version_id
  AND c.included AND c.kind <> 'principal';

-- name: ObservationComponents :many
SELECT *
FROM charge_components
WHERE account_id = @account_id AND observation_id = @observation_id
ORDER BY kind;
