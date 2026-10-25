-- +goose Up
ALTER TABLE accounts ADD COLUMN sandbox_of uuid REFERENCES accounts (id);

CREATE TABLE case_evaluations (
    id                 uuid PRIMARY KEY,
    account_id         uuid NOT NULL REFERENCES accounts (id),
    sandbox_account_id uuid NOT NULL REFERENCES accounts (id),
    credential_kind    text NOT NULL CHECK (credential_kind IN ('api_key', 'oauth_grant')),
    credential_id      uuid NOT NULL,
    scenario_version   integer NOT NULL CHECK (scenario_version > 0),
    started_at         timestamptz NOT NULL,
    expires_at         timestamptz NOT NULL,
    finished_at        timestamptz,
    report             jsonb CHECK (report IS NULL OR jsonb_typeof(report) = 'object'),
    CHECK (expires_at > started_at),
    CHECK ((finished_at IS NULL) = (report IS NULL))
);
CREATE INDEX case_evaluations_credential_idx ON case_evaluations (credential_id) WHERE finished_at IS NULL;
CREATE INDEX case_evaluations_account_idx ON case_evaluations (account_id, started_at);
REVOKE ALL ON case_evaluations FROM lastro_app;

-- +goose Down
DROP TABLE case_evaluations;
ALTER TABLE accounts DROP COLUMN sandbox_of;
