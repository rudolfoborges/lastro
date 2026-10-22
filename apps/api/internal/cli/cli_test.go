package cli_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/rudolfoborges/lastro/apps/api/internal/cli"
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
