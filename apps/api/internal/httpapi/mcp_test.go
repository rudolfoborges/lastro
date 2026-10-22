package httpapi_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rudolfoborges/lastro/apps/api/internal/accounts"
	"github.com/rudolfoborges/lastro/apps/api/internal/config"
	"github.com/rudolfoborges/lastro/apps/api/internal/httpapi"
	"github.com/rudolfoborges/lastro/apps/api/internal/ledger"
	"github.com/rudolfoborges/lastro/apps/api/internal/mcpserver"
	"github.com/rudolfoborges/lastro/apps/api/internal/store"
	"github.com/rudolfoborges/lastro/apps/api/internal/store/storetest"
)

type bearerTransport struct {
	token string
}

func (b bearerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(req)
}

func newMCPServer(t *testing.T) (*httptest.Server, *accounts.Service) {
	t.Helper()
	db, err := store.Open(t.Context(), storetest.MigratedDatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	logger := slog.New(slog.NewTextHandler(&syncBuffer{}, nil))
	accountsService := accounts.NewService(db)
	mcp := mcpserver.New(mcpserver.Deps{Accounts: accountsService, Ledger: ledger.NewService(db), Logger: logger})
	server := httptest.NewServer(httpapi.NewHandler(httpapi.Deps{
		Logger:     logger,
		Database:   db,
		Accounts:   accountsService,
		RateLimits: config.RateLimits{},
		MCP:        mcp.HTTPHandler(),
	}))
	t.Cleanup(server.Close)
	return server, accountsService
}

func keyFor(t *testing.T, svc *accounts.Service, name string, scopes ...string) string {
	t.Helper()
	invite, err := svc.CreateInvite(t.Context(), time.Hour, "")
	if err != nil {
		t.Fatal(err)
	}
	account, _, err := svc.SignUp(t.Context(), invite.Code, name)
	if err != nil {
		t.Fatal(err)
	}
	key, err := svc.CreateKey(t.Context(), account.ID, "case", scopes)
	if err != nil {
		t.Fatal(err)
	}
	return key.Token
}

func TestMCPRejectsRequestsWithoutAValidBearerToken(t *testing.T) {
	server, _ := newMCPServer(t)

	for name, token := range map[string]string{"missing": "", "invalid": "lst_nope"} {
		t.Run(name, func(t *testing.T) {
			req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, server.URL+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Content-Type", "application/json")
			if token != "" {
				req.Header.Set("Authorization", "Bearer "+token)
			}
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = res.Body.Close() }()
			if res.StatusCode != http.StatusUnauthorized || !strings.HasPrefix(res.Header.Get("WWW-Authenticate"), "Bearer") {
				t.Errorf("status = %d, WWW-Authenticate = %q", res.StatusCode, res.Header.Get("WWW-Authenticate"))
			}
		})
	}
}

func TestMCPOverHTTPServesTheAccountOfTheKey(t *testing.T) {
	server, svc := newMCPServer(t)
	token := keyFor(t, svc, "Rudolfo", "expenses:read", "expenses:write")
	client := mcp.NewClient(&mcp.Implementation{Name: "case-test", Version: "1"}, nil)

	session, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{
		Endpoint:   server.URL + "/mcp",
		HTTPClient: &http.Client{Transport: bearerTransport{token: token}},
	}, nil)
	if err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	defer func() { _ = session.Close() }()

	tools, err := session.ListTools(t.Context(), nil)
	if err != nil || len(tools.Tools) == 0 {
		t.Fatalf("ListTools() = %+v, %v", tools, err)
	}
	res, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "list_categories", Arguments: map[string]any{}})
	if err != nil || res.IsError {
		t.Fatalf("CallTool(list_categories) = %+v, %v", res, err)
	}
	text, _ := res.Content[0].(*mcp.TextContent)
	if text == nil || !strings.Contains(text.Text, "groceries") {
		t.Errorf("list_categories = %+v", res.Content)
	}
}
