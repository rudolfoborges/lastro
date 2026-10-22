-- name: CreateInvite :one
INSERT INTO invites (id, code_hash, note, expires_at)
VALUES (@id, @code_hash, @note, @expires_at)
RETURNING id, note, created_at, expires_at;

-- name: RedeemInvite :one
UPDATE invites
SET redeemed_at = now(), account_id = @account_id
WHERE code_hash = @code_hash
  AND redeemed_at IS NULL
  AND expires_at > now()
RETURNING id;
