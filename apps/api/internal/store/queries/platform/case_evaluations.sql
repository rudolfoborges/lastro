-- name: CreateSandboxAccount :one
INSERT INTO accounts (id, name, timezone, statement_month, sandbox_of)
SELECT @id, @name, owner.timezone, owner.statement_month, owner.id
FROM accounts owner
WHERE owner.id = @owner_id
RETURNING *;

-- name: InsertCaseEvaluation :one
INSERT INTO case_evaluations (id, account_id, sandbox_account_id, credential_kind, credential_id, scenario_version, started_at, expires_at)
VALUES (@id, @account_id, @sandbox_account_id, @credential_kind, @credential_id, @scenario_version, @started_at, @expires_at)
RETURNING *;

-- name: UnfinishedCaseEvaluation :one
SELECT *
FROM case_evaluations
WHERE credential_id = @credential_id AND credential_kind = @credential_kind AND account_id = @account_id
  AND finished_at IS NULL
ORDER BY started_at DESC
LIMIT 1;

-- name: OpenCaseEvaluation :one
SELECT *
FROM case_evaluations
WHERE credential_id = @credential_id AND credential_kind = @credential_kind AND account_id = @account_id AND finished_at IS NULL
ORDER BY started_at DESC
LIMIT 1
FOR UPDATE;

-- name: FinishCaseEvaluation :one
UPDATE case_evaluations
SET finished_at = @finished_at, report = @report
WHERE id = @id AND finished_at IS NULL
RETURNING *;

-- name: ListCaseEvaluations :many
SELECT *
FROM case_evaluations
WHERE account_id = @account_id
ORDER BY started_at DESC
LIMIT @row_limit;
