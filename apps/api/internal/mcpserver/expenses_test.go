package mcpserver_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rudolfoborges/lastro/apps/api/internal/mcpserver"
)

var chatgpt = mcp.Meta{"openai/session": "v1/conversation-abc"}

func supermarket(requestID string) map[string]any {
	return map[string]any{
		"request_id":     requestID,
		"raw_text":       "Gastei R$ 327,39 no mercado agora",
		"amount":         "327.39",
		"description":    "Compra no mercado",
		"date_reference": "now",
		"category":       "groceries",
	}
}

func TestAcceptanceCriterion1RecordingByConversation(t *testing.T) {
	e := newEnv(t)
	session := e.session(e.principal("Rudolfo", "expenses:read", "expenses:write"))

	first := callTool[mcpserver.RecordExpenseOutput](t, session, "record_expense", supermarket("message-1"), chatgpt)
	if first.isError || first.out.Outcome != "completed" || first.out.Result != "recorded" {
		t.Fatalf("first message = %+v", first)
	}
	recorded := first.out.Transaction
	if recorded.Amount.Amount != "327.39" || recorded.Amount.Currency != "BRL" || recorded.Categories[0].Category != "groceries" || recorded.Categories[0].Label != "Mercado" {
		t.Errorf("recorded = %+v", recorded)
	}
	if *recorded.Date.On != "2026-10-21" || recorded.Date.Source != "inferred" {
		t.Errorf("date = %+v, want 21/10 inferred from 23:50 in São Paulo", recorded.Date)
	}
	wantSummary := `Registrei R$ 327,39 em "Compra no mercado", categoria Mercado (sugerida), 21/10/2026 (data inferida). Situação: provisória. Falta: meio de pagamento.`
	if first.out.Summary != wantSummary {
		t.Errorf("summary = %q\nwant      %q", first.out.Summary, wantSummary)
	}

	resent := callTool[mcpserver.RecordExpenseOutput](t, session, "record_expense", supermarket("message-1"), chatgpt)
	if resent.out.Result != "duplicate_of" || resent.out.Transaction.ID != recorded.ID || !strings.Contains(resent.out.Summary, "Nada novo foi criado") {
		t.Errorf("same message again = %+v", resent.out)
	}

	retyped := callTool[mcpserver.RecordExpenseOutput](t, session, "record_expense", supermarket("message-2"), chatgpt)
	if retyped.out.Outcome != "pending" || retyped.out.Result != "possible_duplicate" || retyped.out.Transaction.ID != recorded.ID {
		t.Errorf("same text with a new request = %+v", retyped.out)
	}
	confirmed := supermarket("message-2")
	confirmed["confirm_distinct"] = true
	second := callTool[mcpserver.RecordExpenseOutput](t, session, "record_expense", confirmed, chatgpt)
	if second.out.Result != "recorded" || second.out.Transaction.ID == recorded.ID {
		t.Errorf("second real purchase = %+v", second.out)
	}

	another := supermarket("message-3")
	another["raw_text"] = "Passei no mercado de novo, R$ 327,39"
	third := callTool[mcpserver.RecordExpenseOutput](t, session, "record_expense", another, chatgpt)
	if third.out.Result != "recorded" {
		t.Errorf("another message with the same amount = %+v", third.out)
	}
}

func TestARecordWithoutPaymentMethodIsProvisionalWithThePendingField(t *testing.T) {
	e := newEnv(t)
	session := e.session(e.principal("Rudolfo", "expenses:write"))
	args := supermarket("message-1")
	delete(args, "category")

	got := callTool[mcpserver.RecordExpenseOutput](t, session, "record_expense", args, nil)

	if got.out.Transaction.Status != "provisional" || !slices.Contains(got.out.Pending, "payment_method") || !slices.Contains(got.out.Pending, "category") {
		t.Errorf("record = %+v", got.out)
	}
	if got.out.Transaction.Categories[0].Category != "unidentified" || got.out.Transaction.Categories[0].Status != "missing" {
		t.Errorf("categories = %+v", got.out.Transaction.Categories)
	}
	if len(got.out.Clarifications) != 1 || got.out.Clarifications[0].Kind != "category_unknown" {
		t.Errorf("clarifications = %+v", got.out.Clarifications)
	}
}

func TestPixAndCardDetailsAreRecorded(t *testing.T) {
	e := newEnv(t)
	session := e.session(e.principal("Rudolfo", "expenses:write"))
	pix := supermarket("message-1")
	pix["payment_method"] = "pix"
	card := supermarket("message-2")
	card["raw_text"], card["payment_method"], card["card_last4"] = "Paguei o mercado no cartão final 1234", "credit_card", "1234"

	gotPix := callTool[mcpserver.RecordExpenseOutput](t, session, "record_expense", pix, chatgpt)
	gotCard := callTool[mcpserver.RecordExpenseOutput](t, session, "record_expense", card, chatgpt)

	if gotPix.out.Transaction.Status != "confirmed" || !strings.Contains(gotPix.out.Summary, "Situação: confirmada") || !slices.Contains(gotPix.out.Pending, "purpose") {
		t.Errorf("pix = %+v", gotPix.out)
	}
	if *gotCard.out.Transaction.PaymentMethod.CardLast4 != "1234" || !strings.Contains(gotCard.out.Summary, "provisória até aparecer na fatura") {
		t.Errorf("card = %+v", gotCard.out)
	}
}

func TestTheResponseNeverClaimsSuccessWhenNothingWasRecorded(t *testing.T) {
	e := newEnv(t)
	session := e.session(e.principal("Rudolfo", "expenses:write"))
	tests := map[string]map[string]any{
		"brazilian amount":  {"request_id": "r1", "raw_text": "Gastei R$ 327,39", "amount": "327,39", "description": "Mercado"},
		"unknown currency":  {"request_id": "r2", "raw_text": "Gastei 10", "amount": "10", "currency": "BTC", "description": "Mercado"},
		"bad date":          {"request_id": "r3", "raw_text": "Gastei 10", "amount": "10", "description": "Mercado", "occurred_on": "21/10/2026"},
		"card without kind": {"request_id": "r4", "raw_text": "Gastei 10", "amount": "10", "description": "Mercado", "card_last4": "1234"},
		"unknown category":  {"request_id": "r5", "raw_text": "Gastei 10", "amount": "10", "description": "Mercado", "category": "pets"},
	}
	for name, args := range tests {
		t.Run(name, func(t *testing.T) {
			got := callTool[mcpserver.RecordExpenseOutput](t, session, "record_expense", args, nil)
			if !got.isError || got.toolErr.Outcome != "failed" || got.toolErr.Code != "invalid_request" || got.toolErr.Message == "" {
				t.Errorf("record_expense = %+v", got)
			}
		})
	}

	missing := callTool[mcpserver.RecordExpenseOutput](t, session, "record_expense", map[string]any{"request_id": "r6", "raw_text": "Gastei no mercado", "description": "Mercado"}, nil)
	if missing.out.Outcome != "pending" || missing.out.Result != "needs_clarification" || missing.out.Question != "Qual foi o valor?" || missing.out.Transaction != nil {
		t.Errorf("missing amount = %+v", missing.out)
	}
}
