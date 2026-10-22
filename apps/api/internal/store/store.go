package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/rudolfoborges/lastro/apps/api/internal/store/platformdb"
	"github.com/rudolfoborges/lastro/apps/api/internal/store/tenantdb"
)

const tenantRole = "lastro_app"

type Store struct {
	pool *pgxpool.Pool
}

func Open(ctx context.Context, databaseURL string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse DATABASE_URL: %w", err)
	}
	cfg.ConnConfig.RuntimeParams["timezone"] = "UTC"
	cfg.ConnConfig.RuntimeParams["application_name"] = "lastro"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create connection pool: %w", err)
	}
	return &Store{pool: pool}, nil
}

func NewID() uuid.UUID {
	return uuid.Must(uuid.NewV7())
}

func (s *Store) Ping(ctx context.Context) error {
	return s.pool.Ping(ctx)
}

func (s *Store) Close() {
	s.pool.Close()
}

// Platform queries run as the schema owner and bypass row-level security, so
// they are reserved for operations that happen before an account is known.
func (s *Store) Platform() *platformdb.Queries {
	return platformdb.New(s.pool)
}

func (s *Store) PlatformTx(ctx context.Context, fn func(*platformdb.Queries) error) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		return fn(platformdb.New(tx))
	})
}

func (s *Store) TenantTx(ctx context.Context, accountID uuid.UUID, fn func(*tenantdb.Queries) error) error {
	return s.inTenantTx(ctx, accountID, func(tx pgx.Tx) error {
		return fn(tenantdb.New(tx))
	})
}

func (s *Store) inTenantTx(ctx context.Context, accountID uuid.UUID, fn func(pgx.Tx) error) error {
	if accountID == uuid.Nil {
		return fmt.Errorf("tenant transaction requires an account ID")
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SET LOCAL ROLE "+tenantRole); err != nil {
			return fmt.Errorf("switch to tenant role: %w", err)
		}
		if _, err := tx.Exec(ctx, "SELECT set_config('app.account_id', $1, true)", accountID.String()); err != nil {
			return fmt.Errorf("set tenant: %w", err)
		}
		return fn(tx)
	})
}
