package mcpserver_test

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rudolfoborges/lastro/apps/api/internal/mcpserver"
)

func startEvaluation(t *testing.T, session *mcp.ClientSession) mcpserver.StartCaseEvaluationOutput {
	t.Helper()
	got := callTool[mcpserver.StartCaseEvaluationOutput](t, session, "start_case_evaluation", map[string]any{}, nil)
	if got.isError {
		t.Fatalf("start_case_evaluation = %s", got.text)
	}
	return got.out
}

func record(t *testing.T, session *mcp.ClientSession, args map[string]any) mcpserver.RecordExpenseOutput {
	t.Helper()
	args["request_id"] = uuid.NewString()
	got := callTool[mcpserver.RecordExpenseOutput](t, session, "record_expense", args, chatgpt)
	if got.isError {
		t.Fatalf("record_expense(%v) = %s", args["raw_text"], got.text)
	}
	return got.out
}

func (e *env) handleEvaluationConversation(session *mcp.ClientSession, messages []mcpserver.ConversationMessageOutput) {
	t := e.t
	t.Helper()
	text := map[string]string{}
	for _, message := range messages {
		text[message.ID] = message.Text
	}
	for _, args := range []map[string]any{
		{"raw_text": text["c1"], "amount": "327.39", "description": "Mercado", "date_reference": "now", "category": "groceries", "category_status": "suggested"},
		{"raw_text": text["c2"], "amount": "18.50", "description": "Refrigerante e chocolate na conveniência do posto", "date_reference": "now", "payment_method": "debit_card", "category": "convenience_snacks", "category_status": "suggested"},
		{"raw_text": text["c3"], "amount": "200", "description": "Gasolina", "date_reference": "now", "payment_method": "credit_card", "category": "fuel", "category_status": "suggested"},
		{"raw_text": text["c4"], "amount": "89.90", "description": "Compra na Amazon", "merchant": "Amazon", "date_reference": "now", "payment_method": "credit_card", "category": "unidentified", "category_status": "suggested"},
	} {
		if got := record(t, session, args); got.Result != "recorded" {
			t.Fatalf("record_expense(%s) = %+v", args["raw_text"], got)
		}
	}
	repeated := record(t, session, map[string]any{"raw_text": text["c6"], "amount": "327.39", "description": "Mercado", "date_reference": "now", "category": "groceries", "category_status": "suggested"})
	if repeated.Result != "possible_duplicate" {
		t.Fatalf("repeated message = %+v, want possible_duplicate", repeated)
	}
	pix := record(t, session, map[string]any{"raw_text": text["c7"], "amount": "150", "description": "Pix para João", "merchant": "João", "date_reference": "now", "payment_method": "pix", "category": "unidentified", "category_status": "suggested"})
	if pix.Result != "recorded" {
		t.Fatalf("pix = %+v", pix)
	}
}

func (e *env) handleEvaluationInbox(session *mcp.ClientSession) {
	t := e.t
	t.Helper()
	var bakery string
	for range 10 {
		inbox := pending(t, session)
		if inbox.Pending == 0 {
			return
		}
		next := inbox.Notifications[0]
		interpretation := map[string]any{"observation_id": next.ObservationID}
		switch {
		case strings.Contains(next.Text, "PADARIA") && bakery == "":
			for k, v := range map[string]any{"event_type": "purchase", "amount": "32.97", "merchant": "Padaria Pão Quente", "payment_method": "credit_card", "card_last4": "1234", "category": "meals"} {
				interpretation[k] = v
			}
		case strings.Contains(next.Text, "PADARIA"):
			interpretation["event_type"], interpretation["related_transaction_id"] = "purchase_update", bakery
		case strings.Contains(next.Text, "recusado"):
			interpretation["event_type"] = "declined"
		case strings.Contains(next.Text, "Estorno"):
			for k, v := range map[string]any{"event_type": "refund", "amount": "10.00", "merchant": "Loja Y", "payment_method": "credit_card", "card_last4": "1234"} {
				interpretation[k] = v
			}
		case strings.Contains(next.Text, "dispositivo"):
			interpretation["event_type"] = "notice"
		default:
			interpretation["event_type"] = "unknown"
		}
		got := submit(t, session, interpretation)
		if got.isError || got.out.Results[0].Outcome != "completed" {
			t.Fatalf("interpretation of %q = %+v %s", next.Text, got.out, got.text)
		}
		if interpretation["event_type"] == "purchase" {
			bakery = got.out.Results[0].Transaction.ID
		}
	}
	t.Fatal("the evaluation inbox never emptied")
}

func evaluationStatement() map[string]any {
	item := func(kind, description, amount, date, excerpt string, extra map[string]any) map[string]any {
		out := map[string]any{"kind": kind, "original_description": description, "amount": amount, "date": date, "page": 1, "excerpt": excerpt}
		for k, v := range extra {
			out[k] = v
		}
		return out
	}
	return map[string]any{
		"issuer": "Banco Exemplo", "card_last4": "4321", "closing_date": "2026-10-03", "due_date": "2026-10-10",
		"period_start": "2026-09-04", "period_end": "2026-10-03", "total": "1167.95", "previous_balance": "0.00",
		"reading": "complete", "page_count": 1,
		"items": []any{
			item("purchase", "SUPERMERCADO EXEMPLO", "152.30", "2026-09-14", "14/09 SUPERMERCADO EXEMPLO 152,30", map[string]any{"description": "Supermercado", "category": "groceries"}),
			item("installment", "LOJA DE MOVEIS 03/10", "100.00", "2026-07-02", "02/07 LOJA DE MOVEIS 03/10 100,00", map[string]any{"installment_number": 3, "installment_count": 10, "category": "home"}),
			item("pix_installment", "PIX PARCELADO MARIA 02/06", "500.00", "2026-08-10", "10/08 PIX PARCELADO MARIA 02/06 500,00 Principal 450,00 Juros 50,00", map[string]any{
				"installment_number": 2, "installment_count": 6,
				"components": []any{
					map[string]any{"kind": "principal", "amount": "450.00", "included": true},
					map[string]any{"kind": "interest", "amount": "50.00", "included": true},
				},
			}),
			item("pix_installment", "PIX PARCELADO JOSE 01/03", "300.00", "2026-09-05", "05/09 PIX PARCELADO JOSE 01/03 300,00", map[string]any{"installment_number": 1, "installment_count": 3}),
			item("purchase", "EXAMPLE APP", "110.00", "2026-09-20", "20/09 EXAMPLE APP USD 20,00 110,00", map[string]any{"description": "Aplicativo", "original_amount": "20.00", "original_currency": "USD", "category": "software_subscriptions"}),
			item("tax", "IOF TRANSACAO EXTERIOR", "3.85", "2026-09-20", "20/09 IOF TRANSACAO EXTERIOR 3,85", map[string]any{"category": "financial_charges"}),
			item("purchase", "UBER *TRIP", "15.90", "2026-09-22", "22/09 UBER *TRIP 15,90", map[string]any{"description": "Uber", "category": "transport"}),
			item("purchase", "UBER *TRIP", "15.90", "2026-09-22", "22/09 UBER *TRIP 15,90", map[string]any{"description": "Uber", "category": "transport"}),
			item("refund", "ESTORNO LOJA EXEMPLO", "30.00", "2026-09-25", "25/09 ESTORNO LOJA EXEMPLO -30,00", nil),
		},
	}
}

func TestCaseEvaluationRunsInASandboxAndGradesEachFlow(t *testing.T) {
	e := newEnv(t)
	session := e.session(e.principal("Rudolfo", "expenses:read", "expenses:write", "statements:write"))
	record(t, session, map[string]any{"raw_text": "Gastei 42 na farmácia hoje", "amount": "42", "description": "Farmácia", "date_reference": "today", "payment_method": "pix", "purpose": "remédio"})

	started := startEvaluation(t, session)
	again := startEvaluation(t, session)

	if started.Outcome != "completed" || len(started.Conversation) != 7 || !strings.Contains(started.Statement, "final 4321") || started.ExpiresAt != clock.Add(2*time.Hour).Format(time.RFC3339) {
		t.Fatalf("start = %+v", started)
	}
	if again.EvaluationID != started.EvaluationID || !strings.HasPrefix(again.Summary, "Já havia") {
		t.Errorf("second start = %+v, want the open evaluation", again)
	}
	if listed := callTool[mcpserver.ListTransactionsOutput](t, session, "list_transactions", map[string]any{}, nil); len(listed.out.Transactions) != 0 {
		t.Fatalf("sandbox transactions = %d, want none of Rudolfo's records", len(listed.out.Transactions))
	}
	e.handleEvaluationConversation(session, started.Conversation)
	e.handleEvaluationInbox(session)
	if got := callTool[mcpserver.SubmitStatementExtractionOutput](t, session, "submit_statement_extraction", evaluationStatement(), nil); got.isError {
		t.Fatalf("statement = %s", got.text)
	}

	finished := callTool[mcpserver.FinishCaseEvaluationOutput](t, session, "finish_case_evaluation", map[string]any{}, nil)

	if finished.isError || len(finished.out.Report.Flows) != 4 {
		t.Fatalf("finish = %+v %s", finished.out, finished.text)
	}
	for _, flow := range finished.out.Report.Flows {
		if flow.Rate != 100 || flow.Total == 0 {
			t.Errorf("flow %s = %d/%d", flow.Flow, flow.Passed, flow.Total)
		}
	}
	if len(finished.out.Report.Errors) != 0 || !finished.out.Report.Criterion8 || !strings.Contains(finished.out.Summary, "Nenhum erro") {
		t.Errorf("report = %+v", finished.out)
	}
	listed := callTool[mcpserver.ListTransactionsOutput](t, session, "list_transactions", map[string]any{}, nil)
	if len(listed.out.Transactions) != 1 || listed.out.Transactions[0].Amount.Amount != "42.00" {
		t.Errorf("Rudolfo's transactions after the evaluation = %+v, want only his own", listed.out.Transactions)
	}
	reports := callTool[mcpserver.GetCaseEvaluationOutput](t, session, "get_case_evaluation", map[string]any{}, nil)
	if len(reports.out.Reports) != 1 || reports.out.Reports[0].EvaluationID != started.EvaluationID || reports.out.Summary != finished.out.Summary {
		t.Errorf("get_case_evaluation = %+v", reports.out)
	}
}

func TestAnEmptyCaseEvaluationScoresNothing(t *testing.T) {
	e := newEnv(t)
	session := e.session(e.principal("Rudolfo", "expenses:read", "expenses:write"))
	startEvaluation(t, session)

	finished := callTool[mcpserver.FinishCaseEvaluationOutput](t, session, "finish_case_evaluation", map[string]any{}, nil)
	twice := callTool[mcpserver.FinishCaseEvaluationOutput](t, session, "finish_case_evaluation", map[string]any{}, nil)

	if finished.isError {
		t.Fatalf("finish = %s", finished.text)
	}
	total := 0
	for _, flow := range finished.out.Report.Flows {
		total += flow.Total
		if flow.Passed != 0 {
			t.Errorf("flow %s passed %d checks with nothing done", flow.Flow, flow.Passed)
		}
	}
	if len(finished.out.Report.Errors) != total || finished.out.Report.Criterion8 {
		t.Errorf("report = %+v", finished.out.Report)
	}
	if !twice.isError || twice.toolErr.Code != "no_evaluation" {
		t.Errorf("second finish = %+v %s", twice.out, twice.text)
	}
}

func TestAnExpiredEvaluationRefusesCallsUntilItIsFinished(t *testing.T) {
	e := newEnv(t)
	session := e.session(e.principal("Rudolfo", "expenses:read", "expenses:write"))
	first := startEvaluation(t, session)
	e.now = clock.Add(2*time.Hour + time.Minute)

	refused := callTool[mcpserver.ListTransactionsOutput](t, session, "list_transactions", map[string]any{}, nil)
	restart := callTool[mcpserver.StartCaseEvaluationOutput](t, session, "start_case_evaluation", map[string]any{}, nil)
	finished := callTool[mcpserver.FinishCaseEvaluationOutput](t, session, "finish_case_evaluation", map[string]any{}, nil)
	listed := callTool[mcpserver.ListTransactionsOutput](t, session, "list_transactions", map[string]any{}, nil)
	second := startEvaluation(t, session)

	if !refused.isError || refused.toolErr.Code != "evaluation_expired" || !restart.isError || restart.toolErr.Code != "evaluation_expired" {
		t.Errorf("calls after expiry = %s / %s, want evaluation_expired", refused.text, restart.text)
	}
	if finished.isError || finished.out.Report.EvaluationID != first.EvaluationID || listed.isError {
		t.Errorf("finish after expiry = %+v %s; list = %s", finished.out, finished.text, listed.text)
	}
	if second.EvaluationID == first.EvaluationID {
		t.Error("a new start reused the finished evaluation")
	}
}

func TestEvaluationsStayWithTheirOwnCredential(t *testing.T) {
	e := newEnv(t)
	principal := e.principal("Rudolfo", "expenses:read", "expenses:write")
	key, err := e.accounts.CreateKey(t.Context(), principal.AccountID, "phone-test", []string{"expenses:read"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := e.accounts.Authenticate(t.Context(), key.Token)
	if err != nil {
		t.Fatal(err)
	}
	record(t, e.session(principal), map[string]any{"raw_text": "Gastei 42 na farmácia", "amount": "42", "description": "Farmácia"})
	startEvaluation(t, e.session(principal))

	listed := callTool[mcpserver.ListTransactionsOutput](t, e.session(other), "list_transactions", map[string]any{}, nil)

	if len(listed.out.Transactions) != 1 {
		t.Errorf("another credential of Rudolfo sees %d transactions, want his own record", len(listed.out.Transactions))
	}
}
