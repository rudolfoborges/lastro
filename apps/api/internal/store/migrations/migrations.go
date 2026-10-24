package migrations

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

//go:embed *.sql
var files embed.FS

type Migrator struct {
	db       *sql.DB
	provider *goose.Provider
}

func Open(databaseURL string) (*Migrator, error) {
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("create migration lock: %w", err)
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, db, files, goose.WithSessionLocker(locker))
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("load migrations: %w", err)
	}
	return &Migrator{db: db, provider: provider}, nil
}

func (m *Migrator) Up(ctx context.Context) ([]*goose.MigrationResult, error) {
	return m.provider.Up(ctx)
}

func (m *Migrator) UpTo(ctx context.Context, version int64) ([]*goose.MigrationResult, error) {
	return m.provider.UpTo(ctx, version)
}

func (m *Migrator) Down(ctx context.Context) (*goose.MigrationResult, error) {
	return m.provider.Down(ctx)
}

func (m *Migrator) Reset(ctx context.Context) ([]*goose.MigrationResult, error) {
	return m.provider.DownTo(ctx, 0)
}

func (m *Migrator) Status(ctx context.Context) ([]*goose.MigrationStatus, error) {
	return m.provider.Status(ctx)
}

func (m *Migrator) Close() error {
	return m.db.Close()
}

func Fingerprint() (string, error) {
	hash := sha256.New()
	err := fs.WalkDir(files, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		content, err := files.ReadFile(path)
		if err != nil {
			return err
		}
		fmt.Fprintf(hash, "%s\x00%d\x00", path, len(content))
		hash.Write(content)
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("fingerprint migrations: %w", err)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
