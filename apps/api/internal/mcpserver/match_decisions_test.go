package mcpserver_test

import (
	"testing"
	"time"

	"github.com/rudolfoborges/lastro/apps/api/internal/ledger"
	"github.com/rudolfoborges/lastro/apps/api/internal/mcpserver"
)

func TestCaseConfirmsRejectsAndUndoesMatches(t *testing.T) {
	e := newEnv(t)
	principal := e.principal("Rudolfo", "expenses:read", "expenses:write")
	session := e.session(principal)
	told := supermarket("message-1")
	told["amount"], told["raw_text"] = "33.00", "Gastei uns 33 na padaria"
	recorded := callTool[mcpserver.RecordExpenseOutput](t, session, "record_expense", told, chatgpt).out.Transaction
	e.notify(principal, ledger.NotificationPosted, "Compra de R$ 32,97", clock.Add(-time.Hour))
	observation := pending(t, session).Notifications[0].ObservationID
	notified := submit(t, session, map[string]any{"observation_id": observation, "event_type": "purchase", "amount": "32.97", "merchant": "Padaria Pão Quente"}).out.Results[0].Transaction

	decision := map[string]any{"transaction_id": recorded.ID, "other_transaction_id": notified.ID, "request_id": "decision-1", "reason": "Rudolfo arredondou o valor"}
	confirmed := callTool[mcpserver.ConfirmMatchOutput](t, session, "confirm_match", decision, nil)
	detail := callTool[mcpserver.TransactionDetailOutput](t, session, "get_transaction", map[string]any{"transaction_id": notified.ID}, nil).out
	var conversation string
	for _, evidence := range detail.Evidence {
		if evidence.Source == "conversation" && evidence.Status == "confirmed" {
			conversation = evidence.ObservationID
		}
	}
	undone := callTool[mcpserver.UndoMatchOutput](t, session, "undo_match", map[string]any{"transaction_id": notified.ID, "observation_id": conversation, "request_id": "undo-1", "reason": "Eram compras diferentes"}, nil)
	decision["request_id"] = "decision-2"
	rejected := callTool[mcpserver.RejectMatchOutput](t, session, "reject_match", decision, nil)

	if confirmed.isError || confirmed.out.Transaction.ID != notified.ID || confirmed.out.Merged.Status != "merged" {
		t.Fatalf("confirm_match = %+v (%s)", confirmed.out, confirmed.text)
	}
	if undone.isError || undone.out.Effect != "unmerged" || len(undone.out.Transactions) != 2 {
		t.Fatalf("undo_match = %+v (%s)", undone.out, undone.text)
	}
	if rejected.isError || len(rejected.out.Transactions) != 2 {
		t.Errorf("reject_match = %+v (%s)", rejected.out, rejected.text)
	}
	if got := e.count("transaction_merges"); got != 1 {
		t.Errorf("merges = %d, want the merge kept as history", got)
	}
}

func TestCaseCanDeferANotificationMatchToRudolfo(t *testing.T) {
	e := newEnv(t)
	principal := e.principal("Rudolfo", "expenses:read", "expenses:write")
	session := e.session(principal)
	told := supermarket("message-1")
	told["amount"], told["raw_text"] = "32.97", "Gastei R$ 32,97 na padaria agora"
	callTool[mcpserver.RecordExpenseOutput](t, session, "record_expense", told, chatgpt)
	e.notify(principal, ledger.NotificationPosted, "Compra de R$ 32,97", clock.Add(time.Minute))
	observation := pending(t, session).Notifications[0].ObservationID

	got := submit(t, session, map[string]any{"observation_id": observation, "event_type": "purchase", "amount": "32.97", "merchant": "Padaria Pão Quente", "defer_match": true})
	questions := callTool[mcpserver.ListClarificationsOutput](t, session, "list_clarifications", map[string]any{"kind": "possible_match"}, nil)

	item := got.out.Results[0]
	if item.Outcome != "completed" || item.Effect != "created" || item.Transaction.Status != "pending_clarification" {
		t.Fatalf("submission = %+v", item)
	}
	if questions.isError || len(questions.out.Clarifications) != 1 {
		t.Errorf("list_clarifications = %+v (%s)", questions.out, questions.text)
	}
}

func TestCaseSeesTheInstallmentPlanOfAPurchase(t *testing.T) {
	e := newEnv(t)
	session := e.session(e.principal("Rudolfo", "expenses:read", "expenses:write"))
	told := supermarket("message-1")
	told["amount"], told["raw_text"], told["installment_count"], told["installment_amount"] = "1200.00", "TV de 1.200 em 12x de 100", 12, "100.00"
	recorded := callTool[mcpserver.RecordExpenseOutput](t, session, "record_expense", told, chatgpt).out.Transaction

	got := callTool[mcpserver.TransactionDetailOutput](t, session, "get_transaction", map[string]any{"transaction_id": recorded.ID}, nil)

	plan := got.out.InstallmentPlan
	if got.isError || plan == nil || plan.Count != 12 || plan.Total == nil || plan.Total.Amount != "1200.00" || plan.Remaining.Amount != "1200.00" || len(plan.Installments) != 12 {
		t.Fatalf("get_transaction = %+v (%s)", got.out, got.text)
	}
	if plan.Installments[0].State != "known_future" || got.out.Transaction.AmountBasis != "total" {
		t.Errorf("plan = %+v, transaction = %+v", plan, got.out.Transaction)
	}
}

func TestCaseRecordsARefundAndSeesTheNet(t *testing.T) {
	e := newEnv(t)
	session := e.session(e.principal("Rudolfo", "expenses:read", "expenses:write"))
	bought := supermarket("message-1")
	bought["amount"], bought["raw_text"], bought["payment_method"] = "100.00", "Luminária de 100 no débito", "debit_card"
	purchase := callTool[mcpserver.RecordExpenseOutput](t, session, "record_expense", bought, chatgpt).out.Transaction
	refund := map[string]any{"request_id": "message-2", "raw_text": "A loja devolveu 30", "amount": "30.00", "nature": "refund", "refund_of_id": purchase.ID, "payment_method": "debit_card"}

	recorded := callTool[mcpserver.RecordExpenseOutput](t, session, "record_expense", refund, chatgpt)
	detail := callTool[mcpserver.TransactionDetailOutput](t, session, "get_transaction", map[string]any{"transaction_id": purchase.ID}, nil)

	if recorded.isError || recorded.out.Transaction.Nature != "refund" || recorded.out.Transaction.RefundOf == nil {
		t.Fatalf("record_expense(refund) = %+v (%s)", recorded.out, recorded.text)
	}
	if detail.out.Net == nil || detail.out.Net.Amount != "70.00" || detail.out.Refunded.Amount != "30.00" || len(detail.out.Refunds) != 1 {
		t.Errorf("get_transaction = %+v", detail.out)
	}
}

func TestCaseSavesListsAndAppliesARuleRudolfoAskedFor(t *testing.T) {
	e := newEnv(t)
	session := e.session(e.principal("Rudolfo", "expenses:read", "expenses:write"))
	created := callTool[mcpserver.ClassificationRuleResult](t, session, "create_classification_rule", map[string]any{
		"merchant": "Padaria Pão Quente", "category": "meals", "instruction": "Sempre que for na Padaria Pão Quente, é café da manhã", "request_id": "rule-1",
	}, nil)
	listed := callTool[mcpserver.ListClassificationRulesOutput](t, session, "list_classification_rules", map[string]any{}, nil)
	told := supermarket("message-1")
	delete(told, "category")
	recorded := callTool[mcpserver.RecordExpenseOutput](t, session, "record_expense", told, chatgpt).out.Transaction

	applied := callTool[mcpserver.CorrectionOutput](t, session, "set_category", map[string]any{"transaction_id": recorded.ID, "rule_id": created.out.Rule.ID, "request_id": "classify-1"}, nil)

	if created.isError || len(listed.out.Rules) != 1 || listed.out.Rules[0].Category.Code != "meals" {
		t.Fatalf("create = %+v (%s), list = %+v", created.out, created.text, listed.out)
	}
	if applied.isError || applied.out.Transaction.Category.Code != "meals" || applied.out.Transaction.Category.Origin != "rule" {
		t.Errorf("set_category(rule_id) = %+v (%s)", applied.out, applied.text)
	}
}
