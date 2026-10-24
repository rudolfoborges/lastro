-- name: InsertMatchRejection :exec
INSERT INTO match_rejections (account_id, low_id, high_id, reason, author)
VALUES (@account_id, @low_id, @high_id, @reason, @author)
ON CONFLICT DO NOTHING;

-- name: IsMatchRejected :one
SELECT EXISTS (
    SELECT FROM match_rejections
    WHERE account_id = @account_id AND low_id = @low_id AND high_id = @high_id
);

-- name: InsertTransactionMerge :one
INSERT INTO transaction_merges (id, account_id, survivor_id, merged_id, merged_status, observation_ids, refund_ids, changes, reason, author)
VALUES (@id, @account_id, @survivor_id, @merged_id, @merged_status, @observation_ids, @refund_ids, @changes, @reason, @author)
RETURNING *;

-- name: ActiveMergeOfObservation :one
SELECT *
FROM transaction_merges
WHERE account_id = @account_id AND survivor_id = @survivor_id AND undone_at IS NULL
  AND @observation_id::uuid = ANY(observation_ids)
FOR UPDATE;

-- name: UndoTransactionMerge :exec
UPDATE transaction_merges
SET undone_at = now(), undo_reason = @undo_reason
WHERE account_id = @account_id AND id = @id AND undone_at IS NULL;

-- name: MarkTransactionMerged :one
UPDATE transactions
SET status = 'merged', merged_into_id = @merged_into_id, updated_at = now()
WHERE account_id = @account_id AND id = @id
RETURNING *;

-- name: RestoreMergedTransaction :one
UPDATE transactions
SET status = @status, merged_into_id = NULL, updated_at = now()
WHERE account_id = @account_id AND id = @id AND status = 'merged'
RETURNING *;

-- name: SetTransactionStatus :one
UPDATE transactions
SET status = @status, updated_at = now()
WHERE account_id = @account_id AND id = @id
RETURNING *;

-- name: MoveRefunds :many
UPDATE transactions
SET refund_of_id = @to_id, updated_at = now()
WHERE account_id = @account_id AND refund_of_id = @from_id AND id = ANY(@ids::uuid[])
RETURNING id;

-- name: RefundIDsOf :many
SELECT id
FROM transactions
WHERE account_id = @account_id AND refund_of_id = @refund_of_id
ORDER BY created_at, id;

-- name: ConfirmedEvidenceOf :many
SELECT e.id, e.observation_id, o.source
FROM transaction_evidence e
JOIN observations o ON o.account_id = e.account_id AND o.id = e.observation_id
WHERE e.account_id = @account_id AND e.transaction_id = @transaction_id AND e.status = 'confirmed'
ORDER BY e.created_at, e.id
FOR UPDATE OF e;

-- name: ObservationRevisions :many
SELECT *
FROM revisions
WHERE account_id = @account_id AND entity_type = 'transaction' AND entity_id = @entity_id AND observation_id = @observation_id
ORDER BY created_at, id;

-- name: OpenPossibleMatches :many
SELECT *
FROM clarifications
WHERE account_id = @account_id AND kind = 'possible_match' AND status = 'open'
  AND transaction_id = ANY(@transaction_ids::uuid[])
ORDER BY created_at, id;
