-- +goose Up
UPDATE observations
SET payload = (payload - 'conversation')
    || jsonb_build_object('conversation_hash', encode(sha256(convert_to(payload ->> 'conversation', 'UTF8')), 'hex'))
WHERE source = 'conversation' AND payload ? 'conversation';

-- +goose Down
-- The original ChatGPT conversation IDs cannot be recovered from their hashes.
SELECT 1;
