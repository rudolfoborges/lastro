package ledger

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/rudolfoborges/lastro/apps/api/internal/idempotency"
	"github.com/rudolfoborges/lastro/apps/api/internal/store"
	"github.com/rudolfoborges/lastro/apps/api/internal/store/tenantdb"
)

const uniqueViolation = "23505"

type Service struct {
	store *store.Store
}

func NewService(s *store.Store) *Service {
	return &Service{store: s}
}

type tx struct {
	ctx       context.Context
	q         *tenantdb.Queries
	accountID uuid.UUID
}

func (s *Service) inTx(ctx context.Context, accountID uuid.UUID, fn func(*tx) error) error {
	return s.store.TenantTx(ctx, accountID, func(q *tenantdb.Queries) error {
		return fn(&tx{ctx: ctx, q: q, accountID: accountID})
	})
}

func once[T any](t *tx, operation string, actor Actor, request any, fn func() (T, error)) (T, error) {
	if actor.RequestID == "" {
		return fn()
	}
	result, _, err := idempotency.Do(t.ctx, t.q, t.accountID, operation, actor.RequestID, request, fn)
	return result, err
}

func isUniqueViolation(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == uniqueViolation && pgErr.ConstraintName == constraint
}

func notFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}
