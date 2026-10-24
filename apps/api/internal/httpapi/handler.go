package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/rudolfoborges/lastro/apps/api/internal/accounts"
	"github.com/rudolfoborges/lastro/apps/api/internal/config"
	"github.com/rudolfoborges/lastro/apps/api/internal/ledger"
	"github.com/rudolfoborges/lastro/apps/api/internal/oauth"
)

const readinessTimeout = 2 * time.Second

type Pinger interface {
	Ping(ctx context.Context) error
}

type Deps struct {
	Logger            *slog.Logger
	Database          Pinger
	Accounts          *accounts.Service
	Ledger            *ledger.Service
	RateLimits        config.RateLimits
	TrustProxyHeaders bool
	MCP               http.Handler
	OAuth             *oauth.Server
}

type api struct {
	logger            *slog.Logger
	accounts          *accounts.Service
	ledger            *ledger.Service
	keyLimits         *limiterSet
	clientLimits      *limiterSet
	trustProxyHeaders bool
	oauth             *oauth.Server
}

func NewHandler(deps Deps) http.Handler {
	a := &api{
		logger:            deps.Logger,
		accounts:          deps.Accounts,
		ledger:            deps.Ledger,
		keyLimits:         newLimiterSet(deps.RateLimits.PerKeyPerMinute),
		clientLimits:      newLimiterSet(deps.RateLimits.UnauthenticatedPerMinute),
		trustProxyHeaders: deps.TrustProxyHeaders,
		oauth:             deps.OAuth,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", handleHealthz)
	mux.HandleFunc("GET /readyz", handleReadyz(deps.Logger, deps.Database))

	mux.HandleFunc("POST /v1/accounts", a.handleSignUp)
	mux.HandleFunc("GET /v1/whoami", a.authenticated("", a.handleWhoami))
	mux.HandleFunc("PATCH /v1/account", a.authenticated(accounts.ScopeAdmin, a.handleUpdateAccount))
	mux.HandleFunc("GET /v1/api-keys", a.authenticated(accounts.ScopeAdmin, a.handleListKeys))
	mux.HandleFunc("POST /v1/api-keys", a.authenticated(accounts.ScopeAdmin, a.handleCreateKey))
	mux.HandleFunc("POST /v1/api-keys/{id}/revoke", a.authenticated(accounts.ScopeAdmin, a.handleRevokeKey))

	if deps.Ledger != nil {
		mux.HandleFunc("POST /v1/wallet/notifications", a.authenticated(accounts.ScopeWalletIngest, a.handleWalletNotification))
		mux.HandleFunc("POST /v1/wallet/heartbeat", a.authenticated(accounts.ScopeWalletIngest, a.handleWalletHeartbeat))
	}

	if deps.MCP != nil {
		mux.Handle("/mcp", a.mcpEndpoint(deps.MCP))
	}
	if deps.OAuth != nil {
		deps.OAuth.Routes(mux, a.limitedByClient)
		mux.HandleFunc("GET /v1/oauth/grants", a.authenticated(accounts.ScopeAdmin, a.handleListGrants))
		mux.HandleFunc("POST /v1/oauth/grants/{id}/revoke", a.authenticated(accounts.ScopeAdmin, a.handleRevokeGrant))
	}

	return withRequestID(withAccessLog(deps.Logger)(withRecover(deps.Logger)(mux)))
}

func handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func handleReadyz(logger *slog.Logger, database Pinger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), readinessTimeout)
		defer cancel()
		if err := database.Ping(ctx); err != nil {
			logger.WarnContext(r.Context(), "readiness check failed",
				slog.String("request_id", RequestID(r.Context())),
				slog.String("error", err.Error()),
			)
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	}
}
