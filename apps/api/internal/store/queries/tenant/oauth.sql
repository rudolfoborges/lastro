-- name: ListOAuthGrants :many
SELECT g.id, g.scopes, g.created_at, g.last_used_at, g.revoked_at, c.client_id, c.client_name
FROM oauth_grants g
JOIN oauth_clients c ON c.id = g.client_id
WHERE g.account_id = @account_id
ORDER BY g.created_at, g.id;

-- name: RevokeAccountOAuthGrant :one
UPDATE oauth_grants
SET revoked_at = coalesce(revoked_at, now())
WHERE account_id = @account_id AND id = @id
RETURNING id, scopes, created_at, last_used_at, revoked_at;

-- name: TouchOAuthGrant :exec
UPDATE oauth_grants
SET last_used_at = now()
WHERE account_id = @account_id
  AND id = @id
  AND (last_used_at IS NULL OR last_used_at < now() - interval '1 minute');

-- name: RevokeOAuthGrantsAuthorizedByKey :exec
UPDATE oauth_grants
SET revoked_at = now()
WHERE account_id = @account_id AND api_key_id = @api_key_id AND revoked_at IS NULL;
