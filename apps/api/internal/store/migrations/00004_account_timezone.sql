-- +goose Up
ALTER TABLE accounts
    ADD COLUMN timezone text NOT NULL DEFAULT 'America/Sao_Paulo'
        CHECK (char_length(timezone) BETWEEN 1 AND 64);

-- +goose Down
ALTER TABLE accounts DROP COLUMN timezone;
