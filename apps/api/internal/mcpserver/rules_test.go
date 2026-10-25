package mcpserver_test

import (
	"strings"
	"testing"

	"github.com/rudolfoborges/lastro/apps/api/internal/mcpserver"
)

func TestCaseSuggestsARuleThatOnlyHoldsAfterRudolfoApproves(t *testing.T) {
	e := newEnv(t)
	session := e.session(e.principal("Rudolfo", "expenses:read", "expenses:write"))
	var ids []string
	for _, purchase := range []struct{ id, amount, day string }{{"m-1", "15.00", "2026-10-20"}, {"m-2", "12.50", "2026-10-21"}} {
		got := callTool[mcpserver.RecordExpenseOutput](t, session, "record_expense", map[string]any{
			"request_id": purchase.id, "raw_text": "Café na padaria " + purchase.amount, "amount": purchase.amount, "description": "Café da manhã",
			"merchant": "Padaria Pão Quente", "occurred_on": purchase.day, "category": "meals", "category_status": "confirmed",
		}, chatgpt)
		if got.isError {
			t.Fatalf("record_expense = %s", got.text)
		}
		ids = append(ids, got.out.Transaction.ID)
	}

	suggested := callTool[mcpserver.ClassificationRuleResult](t, session, "suggest_classification_rule", map[string]any{
		"request_id": "s-1", "merchant": "Padaria Pão Quente", "category": "meals", "max_amount": "20.00",
		"rationale": "Você confirmou café da manhã nas duas últimas compras de até R$ 20,00 na Padaria Pão Quente", "based_on": ids,
	}, nil)
	waiting := callTool[mcpserver.ListClassificationRulesOutput](t, session, "list_classification_rules", map[string]any{}, nil)
	approved := callTool[mcpserver.ClassificationRuleResult](t, session, "approve_classification_rule", map[string]any{
		"request_id": "a-1", "rule_id": suggested.out.Rule.ID, "instruction": "Pode criar essa regra",
	}, nil)
	active := callTool[mcpserver.ListClassificationRulesOutput](t, session, "list_classification_rules", map[string]any{}, nil)

	if suggested.isError || suggested.out.Rule.Status != "suggested" || suggested.out.Rule.Conditions == nil || suggested.out.Rule.Conditions.MaxAmount.Amount != "20.00" || !strings.Contains(suggested.out.Summary, "ainda sem valer") {
		t.Fatalf("suggestion = %+v (%s)", suggested.out, suggested.text)
	}
	if len(waiting.out.Rules) != 0 || len(waiting.out.Suggestions) != 1 {
		t.Errorf("before approval = %+v, want only the suggestion", waiting.out)
	}
	if approved.isError || approved.out.Rule.Status != "active" || !strings.Contains(approved.out.Summary, "até R$ 20,00") {
		t.Errorf("approval = %+v (%s)", approved.out, approved.text)
	}
	if len(active.out.Rules) != 1 || len(active.out.Suggestions) != 0 {
		t.Errorf("after approval = %+v, want the active rule", active.out)
	}
}
