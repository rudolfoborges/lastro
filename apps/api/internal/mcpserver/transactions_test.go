package mcpserver_test

import (
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rudolfoborges/lastro/apps/api/internal/mcpserver"
)

func (e *env) recorded(session *mcp.ClientSession, args map[string]any) mcpserver.TransactionOutput {
	e.t.Helper()
	got := callTool[mcpserver.RecordExpenseOutput](e.t, session, "record_expense", args, nil)
	if got.isError || got.out.Result != "recorded" {
		e.t.Fatalf("record_expense = %+v", got)
	}
	return *got.out.Transaction
}

func TestUpdateTransactionKeepsTheOriginalAndTheReason(t *testing.T) {
	e := newEnv(t)
	session := e.session(e.principal("Rudolfo", "expenses:read", "expenses:write"))
	recorded := e.recorded(session, supermarket("message-1"))

	got := callTool[mcpserver.CorrectionOutput](t, session, "update_transaction", map[string]any{
		"transaction_id": recorded.ID,
		"request_id":     "fix-1",
		"reason":         "Rudolfo disse que foi R$ 327,93 no Pão de Açúcar",
		"amount":         "327.93",
		"merchant":       "Pão de Açúcar",
		"date_reference": "yesterday",
	}, nil)

	if got.isError || got.out.Transaction.Amount.Amount != "327.93" || *got.out.Transaction.Merchant != "Pão de Açúcar" || *got.out.Transaction.Date.On != "2026-10-20" {
		t.Fatalf("update = %+v", got)
	}
	if got.out.Summary != `Atualizei o estabelecimento, o valor e a data de "Compra no mercado".` {
		t.Errorf("summary = %q", got.out.Summary)
	}
	detail := callTool[mcpserver.TransactionDetailOutput](t, session, "get_transaction", map[string]any{"transaction_id": recorded.ID}, nil)
	if detail.out.Transaction.OriginalDescription != "Gastei R$ 327,39 no mercado agora" || len(detail.out.Evidence) != 1 || detail.out.Evidence[0].Source != "conversation" {
		t.Errorf("detail = %+v", detail.out)
	}
	var amountRevision *mcpserver.RevisionOutput
	for i, revision := range detail.out.History {
		if revision.Field == "amount" {
			amountRevision = &detail.out.History[i]
		}
	}
	if amountRevision == nil || amountRevision.Author != "user" || amountRevision.Reason != "Rudolfo disse que foi R$ 327,93 no Pão de Açúcar" {
		t.Fatalf("history = %+v", detail.out.History)
	}
	if old, _ := amountRevision.OldValue.(map[string]any); old["amount"] != "327.39" {
		t.Errorf("old amount = %+v", amountRevision.OldValue)
	}
}

func TestSetCategoryConfirmsAndClosesTheQuestion(t *testing.T) {
	e := newEnv(t)
	session := e.session(e.principal("Rudolfo", "expenses:read", "expenses:write"))
	args := supermarket("message-1")
	delete(args, "category")
	recorded := e.recorded(session, args)

	classified := callTool[mcpserver.CorrectionOutput](t, session, "set_category", map[string]any{
		"transaction_id": recorded.ID,
		"request_id":     "classify-1",
		"category":       "convenience_snacks",
	}, nil)

	if classified.isError || classified.out.Transaction.Category.Code != "convenience_snacks" || classified.out.Transaction.Category.Status != "confirmed" {
		t.Fatalf("set_category = %+v", classified)
	}
	if classified.out.Summary != `Classifiquei "Compra no mercado" como Conveniência — bebidas e snacks.` {
		t.Errorf("summary = %q", classified.out.Summary)
	}
	open := callTool[mcpserver.ListClarificationsOutput](t, session, "list_clarifications", map[string]any{"kind": "category_unknown"}, nil)
	if open.out.Count != 0 {
		t.Errorf("category question still open: %+v", open.out)
	}

	unknown := callTool[mcpserver.CorrectionOutput](t, session, "set_category", map[string]any{
		"transaction_id": recorded.ID,
		"request_id":     "classify-2",
		"category":       "pets",
	}, nil)
	if !unknown.isError || unknown.toolErr.Field != "category" || !strings.Contains(unknown.toolErr.Message, "groceries") {
		t.Errorf("unknown category = %+v", unknown)
	}
}

func TestVoidTransactionKeepsTheRecordOutOfListings(t *testing.T) {
	e := newEnv(t)
	session := e.session(e.principal("Rudolfo", "expenses:read", "expenses:write"))
	recorded := e.recorded(session, supermarket("message-1"))

	voided := callTool[mcpserver.CorrectionOutput](t, session, "void_transaction", map[string]any{
		"transaction_id": recorded.ID, "request_id": "void-1", "reason": "registrei por engano",
	}, nil)

	if voided.isError || voided.out.Transaction.Status != "voided" || !strings.Contains(voided.out.Summary, "Anulei R$ 327,39") {
		t.Fatalf("void = %+v", voided)
	}
	active := callTool[mcpserver.ListTransactionsOutput](t, session, "list_transactions", map[string]any{}, nil)
	all := callTool[mcpserver.ListTransactionsOutput](t, session, "list_transactions", map[string]any{"status": "voided"}, nil)
	if active.out.Count != 0 || all.out.Count != 1 {
		t.Errorf("active = %d, voided = %d; want 0 and 1", active.out.Count, all.out.Count)
	}
	again := callTool[mcpserver.CorrectionOutput](t, session, "update_transaction", map[string]any{
		"transaction_id": recorded.ID, "request_id": "fix-1", "reason": "x", "description": "y",
	}, nil)
	if !again.isError || again.toolErr.Code != "transaction_closed" {
		t.Errorf("update after void = %+v", again)
	}
}

func TestListTransactionsFiltersWithoutSumming(t *testing.T) {
	e := newEnv(t)
	session := e.session(e.principal("Rudolfo", "expenses:read", "expenses:write"))
	e.recorded(session, supermarket("message-1"))
	pix := supermarket("message-2")
	pix["raw_text"], pix["description"], pix["payment_method"], pix["occurred_on"] = "Pix de R$ 50 dia 3", "Pix", "pix", "2026-10-03"
	delete(pix, "date_reference")
	delete(pix, "category")
	e.recorded(session, pix)

	tests := []struct {
		args map[string]any
		want int
	}{
		{map[string]any{}, 2},
		{map[string]any{"from": "2026-10-10", "to": "2026-10-31"}, 1},
		{map[string]any{"payment_method": "pix"}, 1},
		{map[string]any{"category": "groceries"}, 1},
		{map[string]any{"category": "unidentified"}, 1},
		{map[string]any{"limit": 1}, 1},
		{map[string]any{"offset": 1}, 1},
	}
	for _, tt := range tests {
		got := callTool[mcpserver.ListTransactionsOutput](t, session, "list_transactions", tt.args, nil)
		if got.isError || got.out.Count != tt.want {
			t.Errorf("list_transactions(%v) = %+v, want %d", tt.args, got, tt.want)
		}
		if strings.Contains(got.text, "total") {
			t.Errorf("list_transactions(%v) returned a total: %s", tt.args, got.text)
		}
	}
}

func TestUpdateTransactionRejectsEmptyAndUnknownCalls(t *testing.T) {
	e := newEnv(t)
	session := e.session(e.principal("Rudolfo", "expenses:read", "expenses:write"))
	recorded := e.recorded(session, supermarket("message-1"))

	empty := callTool[mcpserver.CorrectionOutput](t, session, "update_transaction", map[string]any{"transaction_id": recorded.ID, "request_id": "r1", "reason": "nada"}, nil)
	unknown := callTool[mcpserver.CorrectionOutput](t, session, "update_transaction", map[string]any{"transaction_id": "01a165b5-1435-750f-8130-3e5526ec6609", "request_id": "r2", "reason": "x", "description": "y"}, nil)
	badClear := callTool[mcpserver.CorrectionOutput](t, session, "update_transaction", map[string]any{"transaction_id": recorded.ID, "request_id": "r3", "reason": "x", "clear": []string{"amount"}}, nil)
	cleared := callTool[mcpserver.CorrectionOutput](t, session, "update_transaction", map[string]any{"transaction_id": recorded.ID, "request_id": "r4", "reason": "não sei a data", "clear": []string{"date"}}, nil)

	if !empty.isError || empty.toolErr.Code != "invalid_request" {
		t.Errorf("empty update = %+v", empty)
	}
	if !unknown.isError || unknown.toolErr.Code != "not_found" {
		t.Errorf("unknown transaction = %+v", unknown)
	}
	if !badClear.isError || badClear.toolErr.Field != "clear" {
		t.Errorf("clearing the amount = %+v", badClear)
	}
	if cleared.isError || cleared.out.Transaction.Date.Precision != "unknown" {
		t.Errorf("clearing the date = %+v", cleared)
	}
}
