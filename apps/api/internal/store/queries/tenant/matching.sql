-- name: FindMatchCandidates :many
SELECT t.*
FROM transactions t
LEFT JOIN payment_methods pm ON pm.account_id = t.account_id AND pm.id = t.payment_method_id
WHERE t.account_id = @account_id
  AND t.status NOT IN ('voided', 'merged')
  AND t.nature = @nature
  AND NOT (t.id = ANY(@excluded_ids::uuid[]))
  AND (
      (t.amount_minor = @amount_minor AND t.currency = @currency)
      OR (t.original_amount_minor = @amount_minor AND t.original_currency = @currency)
      OR (t.amount_minor = sqlc.narg(original_amount_minor) AND t.currency = sqlc.narg(original_currency))
      OR (t.original_amount_minor = sqlc.narg(original_amount_minor) AND t.original_currency = sqlc.narg(original_currency))
  )
  AND (cardinality(@payment_kinds::text[]) = 0 OR pm.id IS NULL OR pm.kind = ANY(@payment_kinds::text[]))
  AND (t.occurred_on BETWEEN @from_day::date AND @to_day::date
       OR (t.occurred_on IS NULL AND t.created_at BETWEEN @from_at::timestamptz AND @to_at::timestamptz))
  AND NOT EXISTS (
      SELECT FROM transaction_evidence e
      JOIN observations o ON o.account_id = e.account_id AND o.id = e.observation_id
      WHERE e.account_id = t.account_id AND e.transaction_id = t.id AND e.status = 'confirmed'
        AND o.source = ANY(@excluded_sources::text[])
  )
  AND NOT EXISTS (
      SELECT FROM match_rejections r
      WHERE r.account_id = t.account_id
        AND ((r.low_id = t.id AND r.high_id = ANY(@excluded_ids::uuid[]))
             OR (r.high_id = t.id AND r.low_id = ANY(@excluded_ids::uuid[])))
  )
ORDER BY t.occurred_on NULLS LAST, t.created_at, t.id
LIMIT 5;

-- name: TransactionEvidenceSources :many
SELECT DISTINCT o.source
FROM transaction_evidence e
JOIN observations o ON o.account_id = e.account_id AND o.id = e.observation_id
WHERE e.account_id = @account_id AND e.transaction_id = @transaction_id AND e.status = 'confirmed'
ORDER BY o.source;

-- name: HasSameSourceEvidence :one
SELECT EXISTS (
    SELECT FROM transaction_evidence e
    JOIN observations existing ON existing.account_id = e.account_id AND existing.id = e.observation_id
    JOIN observations incoming ON incoming.account_id = e.account_id AND incoming.id = @observation_id
    LEFT JOIN wallet_notification_versions ev ON ev.account_id = existing.account_id AND ev.observation_id = existing.id
    LEFT JOIN wallet_notification_versions iv ON iv.account_id = incoming.account_id AND iv.observation_id = incoming.id
    WHERE e.account_id = @account_id AND e.transaction_id = @transaction_id AND e.status = 'confirmed'
      AND existing.id <> incoming.id
      AND existing.source = incoming.source
      AND (ev.notification_id IS NULL OR iv.notification_id IS NULL OR ev.notification_id <> iv.notification_id)
      AND NOT (coalesce(existing.payload->>'kind', '') IN ('installment', 'pix_installment')
               AND coalesce(incoming.payload->>'kind', '') IN ('installment', 'pix_installment'))
);

-- name: ConfirmedTransactionOfObservation :one
SELECT transaction_id
FROM transaction_evidence
WHERE account_id = @account_id AND observation_id = @observation_id AND status = 'confirmed';

-- name: UnmatchedCardPurchases :many
SELECT t.*
FROM transactions t
JOIN payment_methods pm ON pm.account_id = t.account_id AND pm.id = t.payment_method_id
WHERE t.account_id = @account_id
  AND t.status IN ('provisional', 'pending_clarification')
  AND t.nature = 'expense'
  AND pm.kind = 'credit_card'
  AND (pm.card_last4 IS NULL OR pm.card_last4 = ANY(@cards::text[]))
  AND t.occurred_on BETWEEN @from_day::date AND @to_day::date
  AND NOT EXISTS (
      SELECT FROM transaction_evidence e
      JOIN observations o ON o.account_id = e.account_id AND o.id = e.observation_id
      WHERE e.account_id = t.account_id AND e.transaction_id = t.id AND e.status = 'confirmed' AND o.source = 'statement_line'
  )
ORDER BY t.occurred_on, t.created_at, t.id
LIMIT 50;

-- name: StatementVersionCards :many
SELECT DISTINCT (o.payload->>'card_last4')::text AS card_last4
FROM statement_lines l
JOIN observations o ON o.account_id = l.account_id AND o.id = l.observation_id
WHERE l.account_id = @account_id AND l.statement_version_id = @statement_version_id
  AND o.payload->>'card_last4' IS NOT NULL
ORDER BY 1;
