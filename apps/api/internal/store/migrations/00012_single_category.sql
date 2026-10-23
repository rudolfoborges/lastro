-- +goose Up
CREATE UNIQUE INDEX category_allocations_one_current_idx ON category_allocations (transaction_id)
    WHERE superseded_at IS NULL;

-- +goose Down
DROP INDEX category_allocations_one_current_idx;
