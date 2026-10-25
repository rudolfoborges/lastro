package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/rudolfoborges/lastro/apps/api/internal/accounts"
	"github.com/rudolfoborges/lastro/apps/api/internal/config"
	"github.com/rudolfoborges/lastro/apps/api/internal/export"
	"github.com/rudolfoborges/lastro/apps/api/internal/httpapi"
	"github.com/rudolfoborges/lastro/apps/api/internal/ledger"
	"github.com/rudolfoborges/lastro/apps/api/internal/logging"
	"github.com/rudolfoborges/lastro/apps/api/internal/mcpserver"
	"github.com/rudolfoborges/lastro/apps/api/internal/oauth"
	"github.com/rudolfoborges/lastro/apps/api/internal/store"
)

func runServe(ctx context.Context, _ []string, env Env) error {
	cfg, err := config.Load(env.Getenv)
	if err != nil {
		return err
	}
	logger := logging.New(env.Stdout, cfg.LogFormat, cfg.LogLevel)

	db, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()

	accountsService := accounts.NewService(db)
	ledgerService := ledger.NewService(db)
	exports := export.NewService(db)
	mcp := mcpserver.New(mcpserver.Deps{Accounts: accountsService, Ledger: ledgerService, Exports: exports, PublicBaseURL: cfg.PublicBaseURL.String(), Logger: logger})
	authorization := oauth.New(db, accountsService, logger, oauth.Config{
		Issuer:              cfg.PublicBaseURL,
		RedirectHosts:       cfg.OAuth.RedirectHosts,
		MetadataHosts:       cfg.OAuth.MetadataHosts,
		DynamicRegistration: cfg.OAuth.DynamicRegistration,
		AllowLoopback:       cfg.Environment != config.Production,
	})
	server := &http.Server{
		Addr: fmt.Sprintf(":%d", cfg.Port),
		Handler: httpapi.NewHandler(httpapi.Deps{
			Logger:            logger,
			Database:          db,
			Accounts:          accountsService,
			Ledger:            ledgerService,
			RateLimits:        cfg.RateLimits,
			TrustProxyHeaders: cfg.TrustProxyHeaders,
			MCP:               mcp.HTTPHandler(),
			OAuth:             authorization,
			Exports:           exports,
		}),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelError),
	}

	serveErr := make(chan error, 1)
	go func() { serveErr <- server.ListenAndServe() }()
	logger.Info("server started", slog.Int("port", cfg.Port), slog.String("environment", string(cfg.Environment)))

	select {
	case err := <-serveErr:
		return fmt.Errorf("http server: %w", err)
	case <-ctx.Done():
	}

	logger.Info("shutting down", slog.Duration("timeout", cfg.ShutdownTimeout))
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cfg.ShutdownTimeout)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	if err := <-serveErr; !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("http server: %w", err)
	}
	logger.Info("server stopped")
	return nil
}
