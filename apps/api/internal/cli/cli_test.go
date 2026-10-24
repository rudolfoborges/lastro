package cli_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/rudolfoborges/lastro/apps/api/internal/accounts"
	"github.com/rudolfoborges/lastro/apps/api/internal/cli"
	"github.com/rudolfoborges/lastro/apps/api/internal/store"
	"github.com/rudolfoborges/lastro/apps/api/internal/store/storetest"
)

func run(t *testing.T, args []string, vars map[string]string) (string, string, error) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	err := cli.Run(context.Background(), args, cli.Env{
		Stdout: &stdout,
		Stderr: &stderr,
		Getenv: func(key string) string { return vars[key] },
	})
	return stdout.String(), stderr.String(), err
}

func TestHelpListsCommands(t *testing.T) {
	stdout, _, err := run(t, []string{"help"}, nil)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !strings.Contains(stdout, "serve") {
		t.Errorf("help output does not list serve: %s", stdout)
	}
}

func TestMissingCommandIsAUsageError(t *testing.T) {
	_, stderr, err := run(t, nil, nil)
	if !errors.Is(err, cli.ErrUsage) {
		t.Fatalf("Run() error = %v, want usage error", err)
	}
	if !strings.Contains(stderr, "Usage: lastro") {
		t.Errorf("stderr does not show usage: %s", stderr)
	}
}

func TestUnknownCommandIsAUsageError(t *testing.T) {
	_, _, err := run(t, []string{"deploy"}, nil)
	if !errors.Is(err, cli.ErrUsage) || !strings.Contains(err.Error(), `"deploy"`) {
		t.Errorf("Run() error = %v, want usage error naming the command", err)
	}
}

func TestServeFailsClearlyWithoutRequiredConfiguration(t *testing.T) {
	_, _, err := run(t, []string{"serve"}, nil)
	if err == nil || !strings.Contains(err.Error(), "DATABASE_URL is required") {
		t.Errorf("Run(serve) error = %v, want missing DATABASE_URL", err)
	}
}

func TestMigrateRequiresAnAction(t *testing.T) {
	for _, args := range [][]string{{"migrate"}, {"migrate", "sideways"}} {
		_, _, err := run(t, args, nil)
		if !errors.Is(err, cli.ErrUsage) {
			t.Errorf("Run(%v) error = %v, want usage error", args, err)
		}
	}
}

func TestMigrateUpThenStatusReportsAppliedMigrations(t *testing.T) {
	vars := map[string]string{"DATABASE_URL": storetest.EmptyDatabaseURL(t)}

	stdout, _, err := run(t, []string{"migrate", "up"}, vars)
	if err != nil {
		t.Fatalf("migrate up error = %v", err)
	}
	if !strings.Contains(stdout, "applied 00001_app_role.sql") {
		t.Errorf("migrate up output = %q", stdout)
	}

	stdout, _, err = run(t, []string{"migrate", "up"}, vars)
	if err != nil || !strings.Contains(stdout, "database is up to date") {
		t.Errorf("second migrate up = %q, %v; want up to date", stdout, err)
	}

	stdout, _, err = run(t, []string{"migrate", "status"}, vars)
	if err != nil {
		t.Fatalf("migrate status error = %v", err)
	}
	if strings.Contains(stdout, "pending") || !strings.Contains(stdout, "applied") {
		t.Errorf("migrate status output = %q, want every migration applied", stdout)
	}
}

func TestInvitesCreatePrintsASingleUseCode(t *testing.T) {
	vars := map[string]string{"DATABASE_URL": storetest.MigratedDatabaseURL(t)}

	stdout, _, err := run(t, []string{"invites", "create", "--expires-in", "1h", "--note", "Rudolfo"}, vars)

	if err != nil {
		t.Fatalf("invites create error = %v", err)
	}
	if !strings.Contains(stdout, "invite code: inv_") || !strings.Contains(stdout, "expires at:") {
		t.Errorf("invites create output = %q", stdout)
	}
}

func TestInvitesRequiresTheCreateActionAndValidFlags(t *testing.T) {
	for _, args := range [][]string{{"invites"}, {"invites", "list"}, {"invites", "create", "--expires-in", "soon"}} {
		_, _, err := run(t, args, nil)
		if !errors.Is(err, cli.ErrUsage) {
			t.Errorf("Run(%v) error = %v, want usage error", args, err)
		}
	}
}

func TestSeedCreatesADevelopmentAccountWithKeys(t *testing.T) {
	vars := map[string]string{"DATABASE_URL": storetest.MigratedDatabaseURL(t)}

	stdout, _, err := run(t, []string{"seed"}, vars)

	if err != nil {
		t.Fatalf("seed error = %v", err)
	}
	for _, name := range []string{"admin", "case", "android"} {
		if !strings.Contains(stdout, name+" ") {
			t.Errorf("seed output does not list the %s key: %q", name, stdout)
		}
	}
	if strings.Count(stdout, "lst_") != 3 {
		t.Errorf("seed output = %q, want three tokens", stdout)
	}
}

func TestSeedRefusesToRunInProduction(t *testing.T) {
	vars := map[string]string{"DATABASE_URL": "postgres://unused/lastro", "LASTRO_ENV": "production"}

	_, _, err := run(t, []string{"seed"}, vars)

	if err == nil || !strings.Contains(err.Error(), "production") {
		t.Errorf("seed in production error = %v, want refusal", err)
	}
}

func TestMCPStdioRequiresAnAPIKey(t *testing.T) {
	_, _, err := run(t, []string{"mcp-stdio"}, map[string]string{"DATABASE_URL": "postgres://localhost/none"})
	if err == nil || !strings.Contains(err.Error(), "LASTRO_API_KEY is required") {
		t.Errorf("Run(mcp-stdio) error = %v, want missing LASTRO_API_KEY", err)
	}
}

func TestOperatorIssuesAKeyForAnExistingAccount(t *testing.T) {
	databaseURL := storetest.MigratedDatabaseURL(t)
	vars := map[string]string{"DATABASE_URL": databaseURL}
	if _, _, err := run(t, []string{"seed"}, vars); err != nil {
		t.Fatal(err)
	}

	listed, _, err := run(t, []string{"accounts", "list"}, vars)
	if err != nil {
		t.Fatalf("accounts list error = %v", err)
	}
	accountID, _, _ := strings.Cut(listed, " ")
	issued, _, err := run(t, []string{"keys", "create", "--account", accountID, "--name", "admin", "--scopes", "admin"}, vars)
	if err != nil {
		t.Fatalf("keys create error = %v", err)
	}

	_, after, found := strings.Cut(issued, "token:  ")
	token, _, _ := strings.Cut(after, "\n")
	db, err := store.Open(t.Context(), databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	principal, err := accounts.NewService(db).Authenticate(t.Context(), token)
	if !found || err != nil || principal.AccountID.String() != accountID || !principal.Has(accounts.ScopeAdmin) {
		t.Errorf("issued %q authenticates as %+v, %v; want an admin key of %s", issued, principal, err, accountID)
	}
}

func TestKeysAndAccountsRejectInvalidArguments(t *testing.T) {
	for _, args := range [][]string{
		{"accounts"},
		{"accounts", "delete"},
		{"keys"},
		{"keys", "create", "--account", "rudolfo", "--name", "admin", "--scopes", "admin"},
		{"keys", "create", "--account", "019a0000-0000-7000-8000-000000000000", "--scopes", "admin"},
	} {
		_, _, err := run(t, args, nil)
		if !errors.Is(err, cli.ErrUsage) {
			t.Errorf("Run(%v) error = %v, want usage error", args, err)
		}
	}
}
