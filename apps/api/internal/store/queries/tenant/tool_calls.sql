-- name: RecordToolCall :exec
INSERT INTO tool_calls (id, account_id, credential_kind, credential_id, tool, outcome, error_code, duration_ms)
VALUES (@id, @account_id, @credential_kind, @credential_id, @tool, @outcome, sqlc.narg(error_code), @duration_ms);
