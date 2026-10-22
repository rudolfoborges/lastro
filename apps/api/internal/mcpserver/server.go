package mcpserver

import (
	"context"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rudolfoborges/lastro/apps/api/internal/accounts"
	"github.com/rudolfoborges/lastro/apps/api/internal/ledger"
)

const (
	serverName        = "lastro"
	principalKey      = "principal"
	tokenInfoLifetime = time.Hour
)

const serverInstructions = `Lastro stores, validates and computes Rudolfo's expenses; you (Case) understand and classify what he says.
- Never invent amounts, dates, merchants, categories or purposes; leave unknown fields out.
- Ask Rudolfo only when the doubt changes the amount or the identity of the purchase; otherwise record and report what is pending.
- Generate one request_id per expense and reuse it on every retry of that expense. Send the message time when you know it.
- Classify with list_categories and the classification policy; send category_status "confirmed" only when Rudolfo said the category himself.
- Confirm to Rudolfo what was recorded and what is pending, using each answer's summary. A failed outcome means nothing was recorded.
- Never compute totals yourself; use Lastro's query tools.
Call get_instructions with topic "conversation" before recording expenses, and "classification" before classifying.`

type Deps struct {
	Accounts *accounts.Service
	Ledger   *ledger.Service
	Logger   *slog.Logger
}

type Server struct {
	deps  Deps
	mcp   *mcp.Server
	fixed *accounts.Principal
	now   func() time.Time
}

type Option func(*Server)

// WithPrincipal serves every call as the given principal, for transports that
// carry no HTTP credentials (stdio and in-memory).
func WithPrincipal(p accounts.Principal) Option {
	return func(s *Server) { s.fixed = &p }
}

func WithClock(now func() time.Time) Option {
	return func(s *Server) { s.now = now }
}

func New(deps Deps, opts ...Option) *Server {
	s := &Server{deps: deps, now: time.Now}
	for _, opt := range opts {
		opt(s)
	}
	s.mcp = mcp.NewServer(&mcp.Implementation{Name: serverName, Title: "Lastro", Version: version()}, &mcp.ServerOptions{
		Instructions: serverInstructions,
		Logger:       deps.Logger,
	})
	s.registerInstructions()
	s.registerCatalog()
	s.registerClarifications()
	s.registerExpenses()
	s.registerTransactions()
	return s
}

func (s *Server) MCP() *mcp.Server {
	return s.mcp
}

func (s *Server) HTTPHandler() http.Handler {
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s.mcp }, &mcp.StreamableHTTPOptions{
		Stateless:    true,
		JSONResponse: true,
		Logger:       s.deps.Logger,
	})
}

func (s *Server) RunStdio(ctx context.Context) error {
	return s.mcp.Run(ctx, &mcp.StdioTransport{})
}

func TokenInfo(p accounts.Principal) *auth.TokenInfo {
	scopes := make([]string, len(p.Scopes))
	for i, scope := range p.Scopes {
		scopes[i] = string(scope)
	}
	return &auth.TokenInfo{
		Scopes:     scopes,
		Expiration: time.Now().Add(tokenInfoLifetime),
		UserID:     p.AccountID.String(),
		Extra:      map[string]any{principalKey: p},
	}
}

func (s *Server) principal(req *mcp.CallToolRequest) (accounts.Principal, bool) {
	if s.fixed != nil {
		return *s.fixed, true
	}
	if req == nil || req.Extra == nil || req.Extra.TokenInfo == nil {
		return accounts.Principal{}, false
	}
	p, ok := req.Extra.TokenInfo.Extra[principalKey].(accounts.Principal)
	return p, ok
}

func version() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev"
	}
	for _, setting := range info.Settings {
		if setting.Key == "vcs.revision" && len(setting.Value) >= 7 {
			return setting.Value[:7]
		}
	}
	return "dev"
}
