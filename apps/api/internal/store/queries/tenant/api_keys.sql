-- name: CreateAPIKey :one
INSERT INTO api_keys (id, account_id, name, token_hash, scopes)
VALUES (@id, @account_id, @name, @token_hash, @scopes)
RETURNING *;

-- name: ListAPIKeys :many
SELECT *
FROM api_keys
WHERE account_id = @account_id
ORDER BY created_at, id;

-- name: GetAPIKey :one
SELECT *
FROM api_keys
WHERE account_id = @account_id AND id = @id;

-- name: CountOtherActiveAdminKeys :one
SELECT count(*)
FROM api_keys
WHERE account_id = @account_id
  AND id <> @id
  AND revoked_at IS NULL
  AND 'admin' = ANY (scopes);

-- name: RevokeAPIKey :one
UPDATE api_keys
SET revoked_at = now()
WHERE account_id = @account_id AND id = @id AND revoked_at IS NULL
RETURNING *;

-- name: TouchAPIKey :exec
UPDATE api_keys
SET last_used_at = now()
WHERE account_id = @account_id
  AND id = @id
  AND (last_used_at IS NULL OR last_used_at < now() - interval '1 minute');
