package cli

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/pressly/goose/v3"

	"github.com/rudolfoborges/lastro/apps/api/internal/config"
	"github.com/rudolfoborges/lastro/apps/api/internal/store/migrations"
)

func runMigrate(ctx context.Context, args []string, env Env) error {
	if len(args) != 1 {
		return fmt.Errorf("%w: migrate expects one of up, down or status", ErrUsage)
	}
	action := args[0]
	if action != "up" && action != "down" && action != "status" {
		return fmt.Errorf("%w: unknown migrate action %q, expected up, down or status", ErrUsage, action)
	}

	cfg, err := config.Load(env.Getenv)
	if err != nil {
		return err
	}
	migrator, err := migrations.Open(cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer func() { _ = migrator.Close() }()

	switch action {
	case "up":
		results, err := migrator.Up(ctx)
		if err != nil {
			return fmt.Errorf("migrate up: %w", err)
		}
		if len(results) == 0 {
			fmt.Fprintln(env.Stdout, "database is up to date")
		}
		printResults(env.Stdout, "applied", results...)
	case "down":
		result, err := migrator.Down(ctx)
		if err != nil {
			return fmt.Errorf("migrate down: %w", err)
		}
		printResults(env.Stdout, "rolled back", result)
	case "status":
		statuses, err := migrator.Status(ctx)
		if err != nil {
			return fmt.Errorf("migrate status: %w", err)
		}
		printStatuses(env.Stdout, statuses)
	}
	return nil
}

func printResults(w io.Writer, verb string, results ...*goose.MigrationResult) {
	for _, result := range results {
		fmt.Fprintf(w, "%s %s in %s\n", verb, result.Source.Path, result.Duration.Round(time.Millisecond))
	}
}

func printStatuses(w io.Writer, statuses []*goose.MigrationStatus) {
	for _, status := range statuses {
		appliedAt := "-"
		if status.State == goose.StateApplied {
			appliedAt = status.AppliedAt.UTC().Format(time.RFC3339)
		}
		fmt.Fprintf(w, "%-8s %-20s %s\n", status.State, appliedAt, status.Source.Path)
	}
}
