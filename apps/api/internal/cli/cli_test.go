package cli_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/rudolfoborges/lastro/apps/api/internal/cli"
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
