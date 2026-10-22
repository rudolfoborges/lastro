-- name: InsertRevision :exec
INSERT INTO revisions (id, account_id, entity_type, entity_id, field, old_value, new_value, reason, author, actor_ref, observation_id)
VALUES (@id, @account_id, @entity_type, @entity_id, @field, @old_value, @new_value, @reason, @author, sqlc.narg(actor_ref), sqlc.narg(observation_id));

-- name: ListRevisions :many
SELECT *
FROM revisions
WHERE account_id = @account_id AND entity_type = @entity_type AND entity_id = @entity_id
ORDER BY created_at, id;
