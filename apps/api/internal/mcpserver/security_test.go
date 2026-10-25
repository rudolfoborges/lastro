package mcpserver_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rudolfoborges/lastro/apps/api/internal/accounts"
	"github.com/rudolfoborges/lastro/apps/api/internal/mcpserver"
)

// minimalArguments fills every required property with a value of its type,
// so the call reaches the scope check instead of failing schema validation.
func minimalArguments(schema any) map[string]any {
	args := map[string]any{}
	object, _ := schema.(map[string]any)
	properties, _ := object["properties"].(map[string]any)
	required, _ := object["required"].([]any)
	for _, name := range required {
		property, _ := properties[name.(string)].(map[string]any)
		if values, ok := property["enum"].([]any); ok && len(values) > 0 {
			args[name.(string)] = values[0]
			continue
		}
		kind := property["type"]
		if kinds, ok := kind.([]any); ok {
			for _, candidate := range kinds {
				if candidate != "null" {
					kind = candidate
				}
			}
		}
		switch kind {
		case "integer", "number":
			args[name.(string)] = 1
		case "boolean":
			args[name.(string)] = false
		case "array":
			args[name.(string)] = []any{}
		case "object":
			args[name.(string)] = map[string]any{}
		default:
			args[name.(string)] = "01a17000-0000-7000-8000-000000000000"
		}
	}
	return args
}

func TestEveryToolRefusesACredentialWithoutItsScope(t *testing.T) {
	e := newEnv(t)
	session := e.session(e.principal("Rudolfo", "wallet:ingest"))
	tools, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}

	open := 0
	for _, tool := range tools.Tools {
		schema, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatal(err)
		}
		var decoded any
		if err := json.Unmarshal(schema, &decoded); err != nil {
			t.Fatal(err)
		}
		got := callTool[map[string]any](t, session, tool.Name, minimalArguments(decoded), nil)
		if tool.Name == "get_instructions" {
			open++
			continue
		}
		if !got.isError || got.toolErr.Code != "forbidden" {
			t.Errorf("%s with a phone key = %+v, want forbidden", tool.Name, got)
		}
	}
	if open != 1 || len(tools.Tools) < 30 {
		t.Errorf("tools = %d, open = %d, want every tool listed and only get_instructions without scope", len(tools.Tools), open)
	}
}

func (e *env) sessionWithLogs(principal accounts.Principal) (*mcp.ClientSession, *bytes.Buffer) {
	e.t.Helper()
	logs := &bytes.Buffer{}
	server := mcpserver.New(mcpserver.Deps{
		Accounts: e.accounts,
		Ledger:   e.ledger,
		Logger:   slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
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
	return session, logs
}

func TestAcceptanceCriterion14OthersSeeNothingAndAnswersCarryNoSecrets(t *testing.T) {
	e := newEnv(t)
	owner, logs := e.sessionWithLogs(e.principal("Rudolfo", "expenses:read", "expenses:write"))
	stranger := e.session(e.principal("Outra pessoa", "expenses:read", "expenses:write"))
	message := supermarket("message-1")
	message["raw_text"] = "Paguei 327,39 no mercado com o cartão 4111 1111 1111 1111, código 482913"

	recorded := callTool[mcpserver.RecordExpenseOutput](t, owner, "record_expense", message, chatgpt)
	detail := callTool[mcpserver.TransactionDetailOutput](t, owner, "get_transaction", map[string]any{"transaction_id": recorded.out.Transaction.ID}, nil)
	theirs := callTool[mcpserver.TransactionDetailOutput](t, stranger, "get_transaction", map[string]any{"transaction_id": recorded.out.Transaction.ID}, nil)
	listed := callTool[mcpserver.ListTransactionsOutput](t, stranger, "list_transactions", map[string]any{}, nil)
	spending := callTool[mcpserver.QuerySpendingOutput](t, stranger, "query_spending", map[string]any{}, nil)

	if !theirs.isError || theirs.toolErr.Code != "not_found" || listed.out.Count != 0 || len(spending.out.Totals) != 0 {
		t.Errorf("another account saw the record: %+v / %+v / %+v", theirs, listed.out, spending.out)
	}
	for name, text := range map[string]string{"record_expense": recorded.text, "get_transaction": detail.text, "logs": logs.String()} {
		if strings.Contains(strings.ReplaceAll(text, " ", ""), "4111111111111111") || strings.Contains(text, "482913") {
			t.Errorf("%s carries the card number or the code: %s", name, text)
		}
	}
	if !strings.Contains(detail.text, "1111") {
		t.Errorf("get_transaction = %s, want the last four digits kept", detail.text)
	}
}
