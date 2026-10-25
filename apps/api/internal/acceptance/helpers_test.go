package acceptance_test

import (
	"maps"
	"strings"
	"time"

	"github.com/rudolfoborges/lastro/apps/api/internal/mcpserver"
)

func (p *person) record(id string, fields map[string]any) mcpserver.RecordExpenseOutput {
	p.s.t.Helper()
	args := maps.Clone(fields)
	args["request_id"] = id
	return use[mcpserver.RecordExpenseOutput](p, "record_expense", args)
}

func (p *person) transactions() []mcpserver.TransactionOutput {
	p.s.t.Helper()
	return use[mcpserver.ListTransactionsOutput](p, "list_transactions", map[string]any{}).Transactions
}

func (p *person) pending() mcpserver.ListPendingNotificationsOutput {
	p.s.t.Helper()
	return use[mcpserver.ListPendingNotificationsOutput](p, "list_pending_notifications", map[string]any{})
}

func (p *person) interpret(observationID string, fields map[string]any) mcpserver.InterpretationResultOutput {
	p.s.t.Helper()
	item := maps.Clone(fields)
	item["observation_id"] = observationID
	out := use[mcpserver.SubmitNotificationInterpretationsOutput](p, "submit_notification_interpretations", map[string]any{"interpretations": []any{item}})
	return out.Results[0]
}

func (p *person) submitStatement(extraction map[string]any) mcpserver.SubmitStatementExtractionOutput {
	p.s.t.Helper()
	return use[mcpserver.SubmitStatementExtractionOutput](p, "submit_statement_extraction", extraction)
}

func (p *person) clarifications(kind string) []mcpserver.ClarificationOutput {
	p.s.t.Helper()
	return use[mcpserver.ListClarificationsOutput](p, "list_clarifications", map[string]any{"kind": kind}).Clarifications
}

// statement is the reading Case submits after reading a statement PDF.
func statement(card, closing, total string, items ...map[string]any) map[string]any {
	lines := make([]any, len(items))
	for i, item := range items {
		lines[i] = item
	}
	return map[string]any{
		"issuer": "Banco Exemplo", "card_last4": card, "closing_date": closing, "due_date": closing[:8] + "10",
		"total": total, "reading": "complete", "page_count": 1, "items": lines,
	}
}

func line(kind, description, amount, date string, extra map[string]any) map[string]any {
	day := date[8:10] + "/" + date[5:7]
	item := map[string]any{
		"kind": kind, "original_description": description, "amount": amount, "date": date,
		"page": 1, "excerpt": day + " " + description + " " + strings.ReplaceAll(amount, ".", ","),
	}
	maps.Copy(item, extra)
	return item
}

func sameMoney(m mcpserver.MoneyOutput, amount string) bool {
	return m.Amount == amount && m.Currency == "BRL"
}

func saoPaulo(value string) time.Time {
	at, err := time.Parse(time.RFC3339, value)
	if err != nil {
		panic(err)
	}
	return at
}
