package store

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/rudolfoborges/lastro/apps/api/internal/store/platformdb"
	"github.com/rudolfoborges/lastro/apps/api/internal/store/storetest"
	"github.com/rudolfoborges/lastro/apps/api/internal/store/tenantdb"
)

const insufficientPrivilege = "42501"

func openStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.Context(), storetest.MigratedDatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

func createAccount(t *testing.T, s *Store, name string) platformdb.Account {
	t.Helper()
	account, err := s.Platform().CreateAccount(t.Context(), platformdb.CreateAccountParams{ID: NewID(), Name: name})
	if err != nil {
		t.Fatalf("CreateAccount() error = %v", err)
	}
	return account
}

func TestTenantReadsItsOwnAccount(t *testing.T) {
	s := openStore(t)
	account := createAccount(t, s, "Alice")

	var got tenantdb.Account
	err := s.TenantTx(t.Context(), account.ID, func(q *tenantdb.Queries) error {
		var err error
		got, err = q.GetAccount(t.Context(), account.ID)
		return err
	})

	if err != nil {
		t.Fatalf("GetAccount() error = %v", err)
	}
	if got.ID != account.ID || got.Name != "Alice" {
		t.Errorf("GetAccount() = %+v, want account %s", got, account.ID)
	}
}

func TestRowLevelSecurityHidesOtherAccountsEvenWhenAQueryAsksForThem(t *testing.T) {
	s := openStore(t)
	alice := createAccount(t, s, "Alice")
	bob := createAccount(t, s, "Bob")

	err := s.TenantTx(t.Context(), alice.ID, func(q *tenantdb.Queries) error {
		_, err := q.GetAccount(t.Context(), bob.ID)
		return err
	})

	if !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("GetAccount(bob) as alice error = %v, want no rows", err)
	}
}

func TestRowLevelSecurityRejectsWritesForAnotherAccount(t *testing.T) {
	s := openStore(t)
	alice := createAccount(t, s, "Alice")

	err := s.inTenantTx(t.Context(), alice.ID, func(tx pgx.Tx) error {
		_, err := tx.Exec(t.Context(), "INSERT INTO accounts (id, name) VALUES ($1, 'Mallory')", NewID())
		return err
	})

	assertSQLState(t, err, insufficientPrivilege)
}

func TestTenantRoleCannotDeleteRows(t *testing.T) {
	s := openStore(t)
	alice := createAccount(t, s, "Alice")

	err := s.inTenantTx(t.Context(), alice.ID, func(tx pgx.Tx) error {
		_, err := tx.Exec(t.Context(), "DELETE FROM accounts WHERE id = $1", alice.ID)
		return err
	})

	assertSQLState(t, err, insufficientPrivilege)
}

func TestTenantTxRequiresAnAccount(t *testing.T) {
	s := openStore(t)

	err := s.TenantTx(t.Context(), uuid.Nil, func(*tenantdb.Queries) error { return nil })

	if err == nil {
		t.Error("TenantTx(uuid.Nil) error = nil, want error")
	}
}

func TestNewIDIsAVersion7UUID(t *testing.T) {
	if version := NewID().Version(); version != 7 {
		t.Errorf("NewID().Version() = %d, want 7", version)
	}
}

func TestQueriesNeverDeleteRows(t *testing.T) {
	deleteStatement := regexp.MustCompile(`(?i)\bDELETE\s+FROM\b`)
	err := filepath.WalkDir("queries", func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if deleteStatement.Match(content) {
			t.Errorf("%s physically deletes rows; change a status column instead", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func assertSQLState(t *testing.T, err error, code string) {
	t.Helper()
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != code {
		t.Errorf("error = %v, want SQLSTATE %s", err, code)
	}
}
