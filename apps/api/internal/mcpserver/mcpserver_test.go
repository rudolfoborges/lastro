package mcpserver_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rudolfoborges/lastro/apps/api/internal/accounts"
	"github.com/rudolfoborges/lastro/apps/api/internal/export"
	"github.com/rudolfoborges/lastro/apps/api/internal/ledger"
	"github.com/rudolfoborges/lastro/apps/api/internal/mcpserver"
	"github.com/rudolfoborges/lastro/apps/api/internal/store"
	"github.com/rudolfoborges/lastro/apps/api/internal/store/storetest"
)

var clock = time.Date(2026, 10, 22, 2, 50, 0, 0, time.UTC)

type env struct {
	t           *testing.T
	databaseURL string
	accounts    *accounts.Service
	ledger      *ledger.Service
	exports     *export.Service
}

func newEnv(t *testing.T) *env {
	t.Helper()
	databaseURL := storetest.MigratedDatabaseURL(t)
	s, err := store.Open(t.Context(), databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return &env{t: t, databaseURL: databaseURL, accounts: accounts.NewService(s), ledger: ledger.NewService(s), exports: export.NewService(s)}
}

func (e *env) principal(name string, scopes ...string) accounts.Principal {
	e.t.Helper()
	invite, err := e.accounts.CreateInvite(e.t.Context(), time.Hour, "")
	if err != nil {
		e.t.Fatal(err)
	}
	account, _, err := e.accounts.SignUp(e.t.Context(), invite.Code, name)
	if err != nil {
		e.t.Fatal(err)
	}
	key, err := e.accounts.CreateKey(e.t.Context(), account.ID, "case", scopes)
	if err != nil {
		e.t.Fatal(err)
	}
	principal, err := e.accounts.Authenticate(e.t.Context(), key.Token)
	if err != nil {
		e.t.Fatal(err)
	}
	return principal
}

func (e *env) session(principal accounts.Principal) *mcp.ClientSession {
	e.t.Helper()
	server := mcpserver.New(mcpserver.Deps{
		Accounts:      e.accounts,
		Ledger:        e.ledger,
		Exports:       e.exports,
		PublicBaseURL: "https://lastro.test",
		Logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
	}, mcpserver.WithPrincipal(principal), mcpserver.WithClock(func() time.Time { return clock }))
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.MCP().Connect(e.t.Context(), serverTransport, nil)
	if err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(func() { _ = serverSession.Close() })
	client := mcp.NewClient(&mcp.Implementation{Name: "case-test", Version: "1"}, nil)
	session, err := client.Connect(e.t.Context(), clientTransport, nil)
	if err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(func() { _ = session.Close() })
	return session
}

func (e *env) toolCalls(principal accounts.Principal, outcome string) int {
	e.t.Helper()
	conn, err := pgx.Connect(e.t.Context(), e.databaseURL)
	if err != nil {
		e.t.Fatal(err)
	}
	defer func() { _ = conn.Close(context.Background()) }()
	var count int
	err = conn.QueryRow(e.t.Context(), "SELECT count(*) FROM tool_calls WHERE account_id = $1 AND outcome = $2", principal.AccountID, outcome).Scan(&count)
	if err != nil {
		e.t.Fatal(err)
	}
	return count
}

type result[T any] struct {
	out     T
	isError bool
	text    string
	toolErr mcpserver.ToolError
}

func callTool[T any](t *testing.T, session *mcp.ClientSession, name string, args any, meta mcp.Meta) result[T] {
	t.Helper()
	res, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: args, Meta: meta})
	if err != nil {
		t.Fatalf("CallTool(%s) protocol error = %v", name, err)
	}
	var r result[T]
	r.isError = res.IsError
	if len(res.Content) > 0 {
		if text, ok := res.Content[0].(*mcp.TextContent); ok {
			r.text = text.Text
		}
	}
	if res.IsError {
		_ = json.Unmarshal([]byte(r.text), &r.toolErr)
		return r
	}
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &r.out); err != nil {
		t.Fatalf("decode %s output %s: %v", name, raw, err)
	}
	return r
}

func TestToolsAreListedWithAnnotationsAndSchemas(t *testing.T) {
	e := newEnv(t)
	session := e.session(e.principal("Rudolfo", "expenses:read", "expenses:write"))

	tools, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]*mcp.Tool{}
	for _, tool := range tools.Tools {
		byName[tool.Name] = tool
	}
	for _, name := range []string{"get_instructions", "list_categories", "save_category"} {
		tool, ok := byName[name]
		if !ok {
			t.Errorf("tool %s is not listed", name)
			continue
		}
		if tool.Description == "" || tool.InputSchema == nil || tool.OutputSchema == nil || tool.Annotations == nil {
			t.Errorf("tool %s lacks description, schemas or annotations: %+v", name, tool)
		}
	}
	if !byName["list_categories"].Annotations.ReadOnlyHint || byName["save_category"].Annotations.ReadOnlyHint {
		t.Error("read-only hints are wrong")
	}
}

func TestInstructionsAreAvailableAsAToolAndAsPrompts(t *testing.T) {
	e := newEnv(t)
	session := e.session(e.principal("Rudolfo", "expenses:read"))

	got := callTool[mcpserver.GetInstructionsOutput](t, session, "get_instructions", map[string]any{"topic": "conversation"}, nil)
	if got.isError || !strings.Contains(got.out.Content, "request_id") {
		t.Fatalf("get_instructions(conversation) = %+v", got)
	}
	invalid := callTool[mcpserver.GetInstructionsOutput](t, session, "get_instructions", map[string]any{"topic": "reports"}, nil)
	if !invalid.isError {
		t.Error("get_instructions(reports) succeeded, want a validation error")
	}

	prompt, err := session.GetPrompt(t.Context(), &mcp.GetPromptParams{Name: "classification_policy"})
	if err != nil {
		t.Fatal(err)
	}
	text, _ := prompt.Messages[0].Content.(*mcp.TextContent)
	if text == nil || !strings.Contains(text.Text, "convenience_snacks") {
		t.Errorf("classification prompt = %+v", prompt)
	}
	initialize := session.InitializeResult()
	if initialize == nil || !strings.Contains(initialize.Instructions, "Never invent") {
		t.Errorf("server instructions = %+v", initialize)
	}
}

func TestListCategoriesReturnsTheAccountTaxonomy(t *testing.T) {
	e := newEnv(t)
	session := e.session(e.principal("Rudolfo", "expenses:read"))

	got := callTool[mcpserver.ListCategoriesOutput](t, session, "list_categories", map[string]any{}, nil)

	if got.isError || len(got.out.Categories) != 14 {
		t.Fatalf("list_categories = %+v", got)
	}
	found := false
	for _, category := range got.out.Categories {
		found = found || (category.Code == "convenience_snacks" && category.Label == "Conveniência — bebidas e snacks")
	}
	if !found {
		t.Errorf("categories = %+v", got.out.Categories)
	}
}

func TestCallsWithoutTheRequiredScopeAreRefusedAndAudited(t *testing.T) {
	e := newEnv(t)
	android := e.principal("Rudolfo", "wallet:ingest")
	session := e.session(android)

	got := callTool[mcpserver.ListCategoriesOutput](t, session, "list_categories", map[string]any{}, nil)

	if !got.isError || got.toolErr.Code != "forbidden" || got.toolErr.Outcome != "failed" || !strings.Contains(got.toolErr.Message, "expenses:read") {
		t.Errorf("list_categories without scope = %+v", got)
	}
	if e.toolCalls(android, "failed") != 1 {
		t.Errorf("failed tool calls audited = %d, want 1", e.toolCalls(android, "failed"))
	}
}

func TestCategoriesSavedByOneAccountStayInIt(t *testing.T) {
	e := newEnv(t)
	alice := e.principal("Alice", "expenses:read", "expenses:write")
	bob := e.principal("Bob", "expenses:read", "expenses:write")
	aliceSession, bobSession := e.session(alice), e.session(bob)

	saved := callTool[mcpserver.SaveCategoryOutput](t, aliceSession, "save_category", map[string]any{"code": "pet_care", "label": "Pets"}, nil)
	if saved.isError || saved.out.Outcome != "completed" || saved.out.Summary == "" {
		t.Fatalf("save_category = %+v", saved)
	}

	aliceCategories := callTool[mcpserver.ListCategoriesOutput](t, aliceSession, "list_categories", map[string]any{}, nil)
	bobCategories := callTool[mcpserver.ListCategoriesOutput](t, bobSession, "list_categories", map[string]any{}, nil)
	if len(aliceCategories.out.Categories) != 15 || len(bobCategories.out.Categories) != 14 {
		t.Errorf("Alice has %d categories and Bob %d, want 15 and 14", len(aliceCategories.out.Categories), len(bobCategories.out.Categories))
	}
	if e.toolCalls(alice, "completed") != 2 {
		t.Errorf("completed tool calls audited = %d, want 2", e.toolCalls(alice, "completed"))
	}
}

func TestDomainValidationBecomesAToolErrorTheAgentCanFix(t *testing.T) {
	e := newEnv(t)
	session := e.session(e.principal("Rudolfo", "expenses:write"))

	got := callTool[mcpserver.SaveCategoryOutput](t, session, "save_category", map[string]any{"code": "Pet Care", "label": "Pets"}, nil)

	if !got.isError || got.toolErr.Code != "invalid_request" || got.toolErr.Field != "code" {
		t.Errorf("save_category(invalid code) = %+v", got)
	}
}
