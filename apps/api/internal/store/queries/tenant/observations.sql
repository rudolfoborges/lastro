-- name: InsertObservation :one
INSERT INTO observations (id, account_id, source, source_key, payload, redactions, fingerprint, received_at)
VALUES (@id, @account_id, @source, @source_key, @payload, @redactions, sqlc.narg(fingerprint), @received_at)
RETURNING *;

-- name: GetObservation :one
SELECT *
FROM observations
WHERE account_id = @account_id AND id = @id;

-- name: InsertInterpretation :one
INSERT INTO observation_interpretations (
    id, account_id, observation_id, version, event_type, amount_minor, currency, merchant,
    occurred_at, occurred_on, date_precision, date_source, payment_method_id, details, author
)
VALUES (
    @id, @account_id, @observation_id,
    (SELECT coalesce(max(version), 0) + 1 FROM observation_interpretations WHERE observation_id = @observation_id),
    @event_type, sqlc.narg(amount_minor), sqlc.narg(currency), sqlc.narg(merchant),
    sqlc.narg(occurred_at), sqlc.narg(occurred_on), @date_precision, @date_source, sqlc.narg(payment_method_id),
    @details, @author
)
RETURNING *;

-- name: ListInterpretations :many
SELECT *
FROM observation_interpretations
WHERE account_id = @account_id AND observation_id = @observation_id
ORDER BY version;
