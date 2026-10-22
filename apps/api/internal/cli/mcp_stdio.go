package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/rudolfoborges/lastro/apps/api/internal/accounts"
	"github.com/rudolfoborges/lastro/apps/api/internal/config"
	"github.com/rudolfoborges/lastro/apps/api/internal/ledger"
	"github.com/rudolfoborges/lastro/apps/api/internal/logging"
	"github.com/rudolfoborges/lastro/apps/api/internal/mcpserver"
	"github.com/rudolfoborges/lastro/apps/api/internal/store"
)

var errMissingAPIKey = errors.New("LASTRO_API_KEY is required: the stdio server acts as the account of that key")

func runMCPStdio(ctx context.Context, _ []string, env Env) error {
	cfg, err := config.Load(env.Getenv)
	if err != nil {
		return err
	}
	token := strings.TrimSpace(env.Getenv("LASTRO_API_KEY"))
	if token == "" {
		return errMissingAPIKey
	}
	logger := logging.New(env.Stderr, cfg.LogFormat, cfg.LogLevel)

	db, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()

	accountsService := accounts.NewService(db)
	principal, err := accountsService.Authenticate(ctx, token)
	if err != nil {
		return fmt.Errorf("LASTRO_API_KEY: %w", err)
	}
	server := mcpserver.New(mcpserver.Deps{Accounts: accountsService, Ledger: ledger.NewService(db), Logger: logger}, mcpserver.WithPrincipal(principal))
	return server.RunStdio(ctx)
}
