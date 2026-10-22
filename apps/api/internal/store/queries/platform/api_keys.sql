-- name: CreateAPIKey :one
INSERT INTO api_keys (id, account_id, name, token_hash, scopes)
VALUES (@id, @account_id, @name, @token_hash, @scopes)
RETURNING *;

-- name: GetAPIKeyCredentials :one
SELECT id, account_id, token_hash, scopes, revoked_at
FROM api_keys
WHERE id = @id;
