package cli

import (
	"archive/zip"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/rudolfoborges/lastro/apps/api/internal/config"
	"github.com/rudolfoborges/lastro/apps/api/internal/export"
	"github.com/rudolfoborges/lastro/apps/api/internal/store"
)

func runExport(ctx context.Context, args []string, env Env) error {
	flags := flag.NewFlagSet("export", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	account := flags.String("account", "", "ID of the account, from accounts list")
	out := flags.String("out", "", "zip file to write")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("%w: %w", ErrUsage, err)
	}
	accountID, err := uuid.Parse(*account)
	if err != nil {
		return fmt.Errorf("%w: --account must be an account ID from accounts list", ErrUsage)
	}
	if *out == "" {
		return fmt.Errorf("%w: --out is required", ErrUsage)
	}
	return withExports(ctx, env, func(exports *export.Service) error {
		doc, err := exports.Dump(ctx, accountID, time.Now())
		if err != nil {
			return err
		}
		file, err := os.Create(*out)
		if err != nil {
			return fmt.Errorf("create %s: %w", *out, err)
		}
		if err := exports.Write(ctx, file, doc); err != nil {
			_ = file.Close()
			return err
		}
		if err := file.Close(); err != nil {
			return fmt.Errorf("close %s: %w", *out, err)
		}
		fmt.Fprintf(env.Stdout, "Exported account %s to %s\n", accountID, *out)
		return nil
	})
}

func runImport(ctx context.Context, args []string, env Env) error {
	flags := flag.NewFlagSet("import", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	path := flags.String("file", "", "zip written by lastro export, or the lastro-export.json inside it")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("%w: %w", ErrUsage, err)
	}
	if *path == "" {
		return fmt.Errorf("%w: --file is required", ErrUsage)
	}
	doc, err := readExport(*path)
	if err != nil {
		return err
	}
	return withExports(ctx, env, func(exports *export.Service) error {
		if err := exports.Restore(ctx, doc); err != nil {
			return err
		}
		fmt.Fprintf(env.Stdout, "Restored account %s exported at %s\n", doc.AccountID, doc.ExportedAt.Format(time.RFC3339))
		return nil
	})
}

func readExport(path string) (export.Document, error) {
	if !strings.HasSuffix(strings.ToLower(path), ".zip") {
		file, err := os.Open(filepath.Clean(path))
		if err != nil {
			return export.Document{}, fmt.Errorf("open %s: %w", path, err)
		}
		defer func() { _ = file.Close() }()
		return export.Read(file)
	}
	archive, err := zip.OpenReader(path)
	if err != nil {
		return export.Document{}, fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = archive.Close() }()
	file, err := archive.Open(export.DocumentName)
	if err != nil {
		return export.Document{}, fmt.Errorf("%s has no %s: %w", path, export.DocumentName, err)
	}
	defer func() { _ = file.Close() }()
	return export.Read(file)
}

func withExports(ctx context.Context, env Env, fn func(*export.Service) error) error {
	cfg, err := config.Load(env.Getenv)
	if err != nil {
		return err
	}
	db, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()
	return fn(export.NewService(db))
}
