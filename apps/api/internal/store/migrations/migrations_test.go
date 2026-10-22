package migrations_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/pressly/goose/v3"

	"github.com/rudolfoborges/lastro/apps/api/internal/store/migrations"
	"github.com/rudolfoborges/lastro/apps/api/internal/store/storetest"
)

func TestMigrationsGoUpAndDownCleanlyOnAnEmptyDatabase(t *testing.T) {
	ctx := t.Context()
	databaseURL := storetest.EmptyDatabaseURL(t)
	migrator, err := migrations.Open(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = migrator.Close() })

	if _, err := migrator.Up(ctx); err != nil {
		t.Fatalf("Up() error = %v", err)
	}
	assertEveryMigrationIs(t, migrator, goose.StateApplied)

	if _, err := migrator.Reset(ctx); err != nil {
		t.Fatalf("Reset() error = %v", err)
	}
	assertEveryMigrationIs(t, migrator, goose.StatePending)
	if tables := userTables(t, databaseURL); len(tables) != 1 || tables[0] != "goose_db_version" {
		t.Errorf("tables after reset = %v, want only goose_db_version", tables)
	}

	if _, err := migrator.Up(ctx); err != nil {
		t.Fatalf("Up() after Reset() error = %v", err)
	}
	assertEveryMigrationIs(t, migrator, goose.StateApplied)
}

func TestFingerprintIsStable(t *testing.T) {
	first, err := migrations.Fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	second, err := migrations.Fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	if first != second || len(first) != 64 {
		t.Errorf("Fingerprint() = %q then %q, want the same SHA-256", first, second)
	}
}

func assertEveryMigrationIs(t *testing.T, migrator *migrations.Migrator, want goose.State) {
	t.Helper()
	statuses, err := migrator.Status(t.Context())
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if len(statuses) == 0 {
		t.Fatal("Status() returned no migrations")
	}
	for _, status := range statuses {
		if status.State != want {
			t.Errorf("%s is %s, want %s", status.Source.Path, status.State, want)
		}
	}
}

func userTables(t *testing.T, databaseURL string) []string {
	t.Helper()
	conn, err := pgx.Connect(t.Context(), databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(context.Background()) }()
	rows, err := conn.Query(t.Context(), "SELECT table_name FROM information_schema.tables WHERE table_schema = 'public' ORDER BY table_name")
	if err != nil {
		t.Fatal(err)
	}
	tables, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	return tables
}
