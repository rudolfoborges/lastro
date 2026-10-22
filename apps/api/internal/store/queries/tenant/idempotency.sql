-- name: LockIdempotencyKey :exec
SELECT pg_advisory_xact_lock(hashtextextended(@lock_name::text, 0));

-- name: GetIdempotencyKey :one
SELECT request_hash, response
FROM idempotency_keys
WHERE account_id = @account_id AND operation = @operation AND key = @key;

-- name: InsertIdempotencyKey :exec
INSERT INTO idempotency_keys (account_id, operation, key, request_hash, response)
VALUES (@account_id, @operation, @key, @request_hash, @response);
