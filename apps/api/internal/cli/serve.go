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
	"github.com/rudolfoborges/lastro/apps/api/internal/httpapi"
	"github.com/rudolfoborges/lastro/apps/api/internal/logging"
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

	server := &http.Server{
		Addr: fmt.Sprintf(":%d", cfg.Port),
		Handler: httpapi.NewHandler(httpapi.Deps{
			Logger:            logger,
			Database:          db,
			Accounts:          accounts.NewService(db),
			RateLimits:        cfg.RateLimits,
			TrustProxyHeaders: cfg.TrustProxyHeaders,
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
