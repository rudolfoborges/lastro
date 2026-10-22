-- +goose Up
-- Roles are cluster-wide: migrations running at the same time in other
-- databases of the cluster may create the role or the membership first.
-- +goose StatementBegin
DO $$
BEGIN
    BEGIN
        CREATE ROLE lastro_app NOLOGIN NOINHERIT NOBYPASSRLS;
    EXCEPTION WHEN duplicate_object OR unique_violation THEN
        NULL;
    END;
    BEGIN
        GRANT lastro_app TO CURRENT_USER;
    EXCEPTION WHEN unique_violation THEN
        NULL;
    END;
END
$$;
-- +goose StatementEnd

GRANT USAGE ON SCHEMA public TO lastro_app;
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT, INSERT, UPDATE ON TABLES TO lastro_app;

CREATE FUNCTION current_account_id() RETURNS uuid
    LANGUAGE sql STABLE
    AS $$ SELECT NULLIF(current_setting('app.account_id', true), '')::uuid $$;

-- +goose Down
DROP FUNCTION current_account_id();
ALTER DEFAULT PRIVILEGES IN SCHEMA public REVOKE SELECT, INSERT, UPDATE ON TABLES FROM lastro_app;
REVOKE USAGE ON SCHEMA public FROM lastro_app;
