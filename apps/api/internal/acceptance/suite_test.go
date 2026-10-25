package acceptance_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rudolfoborges/lastro/apps/api/internal/accounts"
	"github.com/rudolfoborges/lastro/apps/api/internal/config"
	"github.com/rudolfoborges/lastro/apps/api/internal/evaluation"
	"github.com/rudolfoborges/lastro/apps/api/internal/export"
	"github.com/rudolfoborges/lastro/apps/api/internal/httpapi"
	"github.com/rudolfoborges/lastro/apps/api/internal/ledger"
	"github.com/rudolfoborges/lastro/apps/api/internal/mcpserver"
	"github.com/rudolfoborges/lastro/apps/api/internal/store"
	"github.com/rudolfoborges/lastro/apps/api/internal/store/storetest"
)

// clock is Case's time: 21/10/2026 at 23:50 in São Paulo. The phone sends
// the real time in sent_at, as the webhook requires.
var clock = time.Date(2026, 10, 22, 2, 50, 0, 0, time.UTC)

type logBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *logBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *logBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

type suite struct {
	t        *testing.T
	server   *httptest.Server
	accounts *accounts.Service
	ledger   *ledger.Service
	logs     *logBuffer
}

func newSuite(t *testing.T) *suite {
	t.Helper()
	db, err := store.Open(t.Context(), storetest.MigratedDatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	logs := &logBuffer{}
	logger := slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	accountsService, ledgerService, exports := accounts.NewService(db), ledger.NewService(db), export.NewService(db)
	mcp := mcpserver.New(mcpserver.Deps{
		Accounts:      accountsService,
		Ledger:        ledgerService,
		Exports:       exports,
		Evaluations:   evaluation.NewService(db, ledgerService),
		PublicBaseURL: "https://lastro.test",
		Logger:        logger,
	}, mcpserver.WithClock(func() time.Time { return clock }))
	server := httptest.NewServer(httpapi.NewHandler(httpapi.Deps{
		Logger:     logger,
		Database:   db,
		Accounts:   accountsService,
		Ledger:     ledgerService,
		RateLimits: config.RateLimits{},
		MCP:        mcp.HTTPHandler(),
		Exports:    exports,
	}))
	t.Cleanup(server.Close)
	return &suite{t: t, server: server, accounts: accountsService, ledger: ledgerService, logs: logs}
}

// person is an account with Case connected over MCP and the Android app's key.
type person struct {
	s         *suite
	accountID uuid.UUID
	admin     string
	phone     string
	caseKey   string
	session   *mcp.ClientSession
}

type bearer string

func (b bearer) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("Authorization", "Bearer "+string(b))
	return http.DefaultTransport.RoundTrip(req)
}

func (s *suite) signUp(name string) *person {
	s.t.Helper()
	invite, err := s.accounts.CreateInvite(s.t.Context(), time.Hour, "")
	if err != nil {
		s.t.Fatal(err)
	}
	account, admin, err := s.accounts.SignUp(s.t.Context(), invite.Code, name)
	if err != nil {
		s.t.Fatal(err)
	}
	p := &person{s: s, accountID: account.ID, admin: admin.Token}
	p.caseKey = p.key("case", "expenses:read", "expenses:write", "statements:write")
	p.phone = p.key("android", "wallet:ingest")
	p.session = s.connect(p.caseKey)
	return p
}

func (p *person) key(name string, scopes ...string) string {
	p.s.t.Helper()
	key, err := p.s.accounts.CreateKey(p.s.t.Context(), p.accountID, name, scopes)
	if err != nil {
		p.s.t.Fatal(err)
	}
	return key.Token
}

func (s *suite) connect(token string) *mcp.ClientSession {
	s.t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "case-acceptance", Version: "1"}, nil)
	session, err := client.Connect(s.t.Context(), &mcp.StreamableClientTransport{
		Endpoint:   s.server.URL + "/mcp",
		HTTPClient: &http.Client{Transport: bearer(token)},
	}, nil)
	if err != nil {
		s.t.Fatal(err)
	}
	s.t.Cleanup(func() { _ = session.Close() })
	return session
}

// conversation is the openai/session ChatGPT sends with every call of a chat.
var conversation = mcp.Meta{"openai/session": "v1/acceptance-conversation"}

type toolError struct {
	Code    string `json:"error"`
	Field   string `json:"field"`
	Message string `json:"message"`
}

func callTool[T any](t *testing.T, session *mcp.ClientSession, name string, args map[string]any) (T, *toolError) {
	t.Helper()
	var out T
	res, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: args, Meta: conversation})
	if err != nil {
		t.Fatalf("%s: protocol error %v", name, err)
	}
	if res.IsError {
		failure := &toolError{}
		if text, ok := res.Content[0].(*mcp.TextContent); ok {
			if err := json.Unmarshal([]byte(text.Text), failure); err != nil {
				failure.Message = text.Text
			}
		}
		return out, failure
	}
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("%s: decode %s: %v", name, raw, err)
	}
	return out, nil
}

// use calls a tool as Case would and fails the test on a tool error.
func use[T any](p *person, name string, args map[string]any) T {
	p.s.t.Helper()
	out, failure := callTool[T](p.s.t, p.session, name, args)
	if failure != nil {
		p.s.t.Fatalf("%s(%v) failed: %+v", name, args, failure)
	}
	return out
}

func (p *person) track(from time.Time) {
	p.s.t.Helper()
	if _, err := p.s.ledger.SetTracking(p.s.t.Context(), p.accountID, true, nil, from); err != nil {
		p.s.t.Fatal(err)
	}
}

// notify posts a Google Wallet notification through the Android app's webhook.
func (p *person) notify(key, event, text string, postedAt time.Time) int {
	p.s.t.Helper()
	body, err := json.Marshal(map[string]any{
		"device_id":        "galaxy-s24",
		"package":          "com.google.android.apps.walletnfcrel",
		"notification_key": key,
		"event":            event,
		"posted_at":        postedAt.Format(time.RFC3339),
		"title":            "Google Wallet",
		"text":             text,
		"sent_at":          time.Now().Format(time.RFC3339Nano),
	})
	if err != nil {
		p.s.t.Fatal(err)
	}
	return p.s.request(http.MethodPost, "/v1/wallet/notifications", p.phone, body)
}

func (s *suite) request(method, path, token string, body []byte) int {
	s.t.Helper()
	req, err := http.NewRequestWithContext(s.t.Context(), method, s.server.URL+path, bytes.NewReader(body))
	if err != nil {
		s.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	_, _ = io.Copy(io.Discard, res.Body)
	return res.StatusCode
}

func requestID() string {
	return uuid.NewString()
}
