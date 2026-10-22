-- name: RecordAPIRequest :exec
INSERT INTO api_requests (id, account_id, api_key_id, request_id, method, route, status, duration_ms)
VALUES (@id, @account_id, @api_key_id, @request_id, @method, @route, @status, @duration_ms);
