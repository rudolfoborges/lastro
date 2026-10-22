-- name: InsertEvidence :one
INSERT INTO transaction_evidence (id, account_id, transaction_id, observation_id, status, origin, score, reasons)
VALUES (@id, @account_id, @transaction_id, @observation_id, @status, @origin, sqlc.narg(score), @reasons)
RETURNING *;

-- name: GetEvidence :one
SELECT *
FROM transaction_evidence
WHERE account_id = @account_id AND id = @id
FOR UPDATE;

-- name: SetEvidenceStatus :one
UPDATE transaction_evidence
SET status = @status, origin = @origin, updated_at = now()
WHERE account_id = @account_id AND id = @id
RETURNING *;

-- name: ListTransactionEvidence :many
SELECT e.id, e.transaction_id, e.observation_id, e.status, e.origin, e.score, e.reasons, e.created_at, e.updated_at,
       o.source, o.received_at
FROM transaction_evidence e
JOIN observations o ON o.account_id = e.account_id AND o.id = e.observation_id
WHERE e.account_id = @account_id AND e.transaction_id = @transaction_id
ORDER BY e.created_at, e.id;

-- name: SetFieldProvenance :exec
INSERT INTO field_provenance (account_id, transaction_id, field, observation_id, confirmed_by_user)
VALUES (@account_id, @transaction_id, @field, sqlc.narg(observation_id), @confirmed_by_user)
ON CONFLICT (transaction_id, field) DO UPDATE
SET observation_id = excluded.observation_id,
    confirmed_by_user = excluded.confirmed_by_user,
    updated_at = now();

-- name: ListFieldProvenance :many
SELECT *
FROM field_provenance
WHERE account_id = @account_id AND transaction_id = @transaction_id
ORDER BY field;
