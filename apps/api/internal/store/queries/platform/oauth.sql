-- name: GetOAuthClient :one
SELECT *
FROM oauth_clients
WHERE client_id = @client_id;

-- name: CreateOAuthClient :one
INSERT INTO oauth_clients (id, client_id, registration, client_name, redirect_uris, secret_hash)
VALUES (@id, @client_id, 'dynamic', sqlc.narg(client_name), @redirect_uris, sqlc.narg(secret_hash))
RETURNING *;

-- name: SaveMetadataDocumentClient :one
INSERT INTO oauth_clients (id, client_id, registration, client_name, redirect_uris, metadata_fetched_at)
VALUES (@id, @client_id, 'metadata_document', sqlc.narg(client_name), @redirect_uris, now())
ON CONFLICT (client_id) DO UPDATE
SET client_name = excluded.client_name,
    redirect_uris = excluded.redirect_uris,
    metadata_fetched_at = now(),
    updated_at = now()
WHERE oauth_clients.registration = 'metadata_document'
RETURNING *;

-- name: CreateOAuthGrant :one
INSERT INTO oauth_grants (id, account_id, client_id, api_key_id, scopes)
VALUES (@id, @account_id, @client_id, @api_key_id, @scopes)
RETURNING *;

-- name: GetOAuthGrant :one
SELECT *
FROM oauth_grants
WHERE id = @id;

-- name: RevokeOAuthGrant :exec
UPDATE oauth_grants
SET revoked_at = now()
WHERE id = @id AND revoked_at IS NULL;

-- name: CreateAuthorizationCode :exec
INSERT INTO oauth_authorization_codes (id, code_hash, grant_id, client_id, redirect_uri, code_challenge, resource, expires_at)
VALUES (@id, @code_hash, @grant_id, @client_id, @redirect_uri, @code_challenge, @resource, @expires_at);

-- name: GetAuthorizationCode :one
SELECT *
FROM oauth_authorization_codes
WHERE code_hash = @code_hash
FOR UPDATE;

-- name: UseAuthorizationCode :exec
UPDATE oauth_authorization_codes
SET used_at = now()
WHERE id = @id;

-- name: CreateOAuthToken :exec
INSERT INTO oauth_tokens (id, grant_id, kind, token_hash, scopes, resource, expires_at)
VALUES (@id, @grant_id, @kind, @token_hash, @scopes, @resource, @expires_at);

-- name: GetOAuthToken :one
SELECT t.id, t.grant_id, t.kind, t.token_hash, t.scopes, t.resource, t.expires_at, t.used_at,
       g.account_id, g.client_id, g.revoked_at AS grant_revoked_at
FROM oauth_tokens t
JOIN oauth_grants g ON g.id = t.grant_id
WHERE t.id = @id
FOR UPDATE OF t;

-- name: UseRefreshToken :exec
UPDATE oauth_tokens
SET used_at = now()
WHERE id = @id;

-- name: LookupOAuthToken :one
SELECT t.id, t.grant_id, t.kind, t.token_hash, t.scopes, t.resource, t.expires_at,
       g.account_id, g.revoked_at AS grant_revoked_at
FROM oauth_tokens t
JOIN oauth_grants g ON g.id = t.grant_id
WHERE t.id = @id;
