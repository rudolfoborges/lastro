-- name: InsertExportLink :exec
INSERT INTO export_links (id, account_id, token_hash, expires_at)
VALUES (@id, @account_id, @token_hash, @expires_at);

-- name: FindExportLink :one
SELECT account_id
FROM export_links
WHERE token_hash = @token_hash AND expires_at > now();
