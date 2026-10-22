package storetest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/rudolfoborges/lastro/apps/api/internal/store/migrations"
)

const templateLockKey int64 = 0x6c617374726f

var (
	templateOnce sync.Once
	templateName string
	templateErr  error
)

func MigratedDatabaseURL(t testing.TB) string {
	t.Helper()
	baseURL := baseURL(t)
	templateOnce.Do(func() { templateName, templateErr = ensureTemplate(baseURL) })
	if templateErr != nil {
		t.Fatalf("prepare migrated template database: %v", templateErr)
	}
	return createDatabase(t, baseURL, templateName)
}

func EmptyDatabaseURL(t testing.TB) string {
	t.Helper()
	return createDatabase(t, baseURL(t), "template1")
}

func baseURL(t testing.TB) string {
	t.Helper()
	value := os.Getenv("TEST_DATABASE_URL")
	if value != "" {
		return value
	}
	if os.Getenv("CI") != "" {
		t.Fatal("TEST_DATABASE_URL must be set in CI")
	}
	t.Skip("TEST_DATABASE_URL is not set; run `docker compose up -d postgres` and export it to run integration tests")
	return ""
}

func createDatabase(t testing.TB, baseURL, template string) string {
	t.Helper()
	name := "lastro_t_" + randomHex(t)
	err := withConnection(context.Background(), baseURL, func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, fmt.Sprintf("CREATE DATABASE %s TEMPLATE %s", quote(name), quote(template)))
		return err
	})
	if err != nil {
		t.Fatalf("create test database: %v", err)
	}
	t.Cleanup(func() {
		err := withConnection(context.Background(), baseURL, func(ctx context.Context, conn *pgx.Conn) error {
			_, err := conn.Exec(ctx, fmt.Sprintf("DROP DATABASE IF EXISTS %s WITH (FORCE)", quote(name)))
			return err
		})
		if err != nil {
			t.Errorf("drop test database %s: %v", name, err)
		}
	})
	databaseURL, err := withDatabase(baseURL, name)
	if err != nil {
		t.Fatal(err)
	}
	return databaseURL
}

func ensureTemplate(baseURL string) (string, error) {
	fingerprint, err := migrations.Fingerprint()
	if err != nil {
		return "", err
	}
	name := "lastro_tpl_" + fingerprint[:16]
	building := name + "_building"

	err = withConnection(context.Background(), baseURL, func(ctx context.Context, conn *pgx.Conn) error {
		if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", templateLockKey); err != nil {
			return err
		}
		defer func() { _, _ = conn.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", templateLockKey) }()

		var exists bool
		if err := conn.QueryRow(ctx, "SELECT EXISTS (SELECT FROM pg_database WHERE datname = $1)", name).Scan(&exists); err != nil {
			return err
		}
		if exists {
			return nil
		}
		if _, err := conn.Exec(ctx, fmt.Sprintf("DROP DATABASE IF EXISTS %s WITH (FORCE)", quote(building))); err != nil {
			return err
		}
		if _, err := conn.Exec(ctx, "CREATE DATABASE "+quote(building)); err != nil {
			return err
		}
		if err := migrate(ctx, baseURL, building); err != nil {
			return err
		}
		_, err := conn.Exec(ctx, fmt.Sprintf("ALTER DATABASE %s RENAME TO %s", quote(building), quote(name)))
		return err
	})
	if err != nil {
		return "", fmt.Errorf("template %s: %w", name, err)
	}
	return name, nil
}

func migrate(ctx context.Context, baseURL, database string) error {
	databaseURL, err := withDatabase(baseURL, database)
	if err != nil {
		return err
	}
	migrator, err := migrations.Open(databaseURL)
	if err != nil {
		return err
	}
	defer func() { _ = migrator.Close() }()
	_, err = migrator.Up(ctx)
	return err
}

func withConnection(ctx context.Context, databaseURL string, fn func(context.Context, *pgx.Conn) error) error {
	conn, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close(context.Background()) }()
	return fn(ctx, conn)
}

func withDatabase(baseURL, database string) (string, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("TEST_DATABASE_URL must be a postgres:// URL: %w", err)
	}
	u.Path = "/" + database
	return u.String(), nil
}

func quote(identifier string) string {
	return pgx.Identifier{identifier}.Sanitize()
}

func randomHex(t testing.TB) string {
	t.Helper()
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b)
}
