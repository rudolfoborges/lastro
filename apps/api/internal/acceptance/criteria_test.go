package acceptance_test

import (
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rudolfoborges/lastro/apps/api/internal/acceptance"
	"github.com/rudolfoborges/lastro/apps/api/internal/mcpserver"
)

func TestCriterion01RecordingByConversation(t *testing.T) {
	rudolfo := newSuite(t).signUp("Rudolfo")
	message := map[string]any{"raw_text": "Gastei R$ 327,39 no mercado agora", "amount": "327.39", "description": "Mercado", "date_reference": "now", "category": "groceries", "category_status": "suggested"}
	first, second := requestID(), requestID()

	recorded := rudolfo.record(first, message)
	retried := rudolfo.record(first, message)
	repeated := rudolfo.record(second, message)
	message["confirm_distinct"] = true
	distinct := rudolfo.record(second, message)

	if recorded.Result != "recorded" || recorded.Transaction.Category == nil || recorded.Transaction.Category.Code != "groceries" || !sameMoney(recorded.Transaction.Amount, "327.39") {
		t.Fatalf("first message = %+v", recorded)
	}
	if retried.Result != "duplicate_of" || retried.Transaction.ID != recorded.Transaction.ID {
		t.Errorf("retry of the same message = %+v, want the same record", retried)
	}
	if repeated.Result != "possible_duplicate" {
		t.Errorf("the same text again = %+v, want a question before recording", repeated)
	}
	if distinct.Result != "recorded" || distinct.Transaction.ID == recorded.Transaction.ID {
		t.Errorf("a second real purchase = %+v, want it recorded", distinct)
	}
	if got := rudolfo.transactions(); len(got) != 2 {
		t.Errorf("transactions = %d, want 2", len(got))
	}
}

func TestCriterion02RepeatedNotification(t *testing.T) {
	rudolfo := newSuite(t).signUp("Rudolfo")
	postedAt := saoPaulo("2026-10-21T15:04:05-03:00")
	rudolfo.track(postedAt.Add(-24 * time.Hour))
	key := "0|com.google.android.apps.walletnfcrel|7|null|10123"
	for range 3 {
		if status := rudolfo.notify(key, "posted", "Compra de R$ 32,97 em PADARIA PAO QUENTE com Visa •••• 1234", postedAt); status != http.StatusCreated && status != http.StatusOK {
			t.Fatalf("webhook = %d", status)
		}
	}
	rudolfo.notify(key, "updated", "Compra de R$ 32,97 em PADARIA PAO QUENTE com Visa •••• 1234 concluída", postedAt.Add(time.Minute))

	inbox := rudolfo.pending()
	purchase := map[string]any{"event_type": "purchase", "amount": "32.97", "merchant": "Padaria Pão Quente", "payment_method": "credit_card", "card_last4": "1234", "category": "meals"}
	created := rudolfo.interpret(inbox.Notifications[0].ObservationID, purchase)
	again := rudolfo.interpret(inbox.Notifications[0].ObservationID, purchase)
	update := rudolfo.pending().Notifications[0]
	updated := rudolfo.interpret(update.ObservationID, map[string]any{"event_type": "purchase_update", "related_transaction_id": created.Transaction.ID})

	if inbox.Pending != 2 {
		t.Errorf("pending = %d, want the first post and its update", inbox.Pending)
	}
	if created.Effect != "created" || again.Result != "already_applied" || updated.Effect != "updated" || updated.Transaction.ID != created.Transaction.ID {
		t.Errorf("interpretations = %+v / %+v / %+v", created, again, updated)
	}
	if got := rudolfo.transactions(); len(got) != 1 || rudolfo.pending().Pending != 0 {
		t.Errorf("transactions = %d, want one purchase and an empty inbox", len(got))
	}
}

func TestCriterion03SamePurchaseFromThreeSources(t *testing.T) {
	rudolfo := newSuite(t).signUp("Rudolfo")
	postedAt := saoPaulo("2026-10-02T09:15:00-03:00")
	rudolfo.track(postedAt.Add(-24 * time.Hour))
	told := rudolfo.record(requestID(), map[string]any{
		"raw_text": "Paguei 32,97 na padaria dia 2, no crédito final 1234, café da equipe", "amount": "32.97", "description": "Padaria", "merchant": "Padaria Pão Quente",
		"occurred_on": "2026-10-02", "payment_method": "credit_card", "card_last4": "1234", "purpose": "café da equipe", "category": "meals", "category_status": "suggested",
	})
	rudolfo.notify("0|wallet|1", "posted", "Compra de R$ 32,97 em PADARIA PAO QUENTE com Visa •••• 1234", postedAt)

	notification := rudolfo.pending().Notifications[0].ObservationID
	purchase := map[string]any{"event_type": "purchase", "amount": "32.97", "merchant": "Padaria Pão Quente", "payment_method": "credit_card", "card_last4": "1234", "category": "meals"}
	offered := rudolfo.interpret(notification, purchase)
	purchase["related_transaction_id"] = told.Transaction.ID
	linked := rudolfo.interpret(notification, purchase)
	bakery := line("purchase", "PADARIA PAO QUENTE", "32.97", "2026-10-02", map[string]any{"description": "Padaria", "category": "meals"})
	reading := statement("1234", "2026-10-03", "32.97", bakery)
	asked := rudolfo.submitStatement(reading)
	bakery["related_transaction_id"] = told.Transaction.ID
	submitted := rudolfo.submitStatement(reading)

	if offered.Result != "possible_match" || len(offered.Candidates) != 1 || offered.Candidates[0].Transaction.ID != told.Transaction.ID {
		t.Fatalf("notification = %+v, want the conversation record as candidate", offered)
	}
	if linked.Effect != "linked" || asked.Result != "possible_match" || submitted.Lines[0].Effect != "linked" {
		t.Fatalf("links = %+v / %s / %+v", linked, asked.Result, submitted.Lines)
	}
	transactions := rudolfo.transactions()
	if len(transactions) != 1 || transactions[0].Status != "confirmed" {
		t.Fatalf("transactions = %+v, want one confirmed purchase", transactions)
	}
	detail := use[mcpserver.TransactionDetailOutput](rudolfo, "get_transaction", map[string]any{"transaction_id": told.Transaction.ID})
	sources := map[string]bool{}
	for _, evidence := range detail.Evidence {
		sources[evidence.Source] = true
	}
	if len(sources) != 3 {
		t.Errorf("evidence sources = %v, want conversation, notification and statement line", sources)
	}
	spending := use[mcpserver.QuerySpendingOutput](rudolfo, "query_spending", map[string]any{"from": "2026-10-02", "to": "2026-10-02"})
	if !sameMoney(spending.Totals[0].Confirmed.Amount, "32.97") || spending.Totals[0].Confirmed.Count != 1 {
		t.Errorf("spending of the day = %+v, want R$ 32,97 once", spending.Totals)
	}
}

func TestCriterion04LegitimateEqualPurchases(t *testing.T) {
	rudolfo := newSuite(t).signUp("Rudolfo")
	postedAt := saoPaulo("2026-10-02T09:15:00-03:00")
	rudolfo.track(postedAt.Add(-24 * time.Hour))
	rudolfo.notify("0|wallet|1", "posted", "Compra de R$ 32,97 em PADARIA PAO QUENTE com Visa •••• 1234", postedAt)
	rudolfo.notify("0|wallet|2", "posted", "Compra de R$ 32,97 em PADARIA PAO QUENTE com Visa •••• 1234", postedAt.Add(5*time.Minute))
	purchase := map[string]any{"event_type": "purchase", "amount": "32.97", "merchant": "Padaria Pão Quente", "payment_method": "credit_card", "card_last4": "1234", "category": "meals"}

	var ids []string
	for _, notification := range rudolfo.pending().Notifications {
		got := rudolfo.interpret(notification.ObservationID, purchase)
		if got.Result == "possible_match" {
			distinct := map[string]any{"confirm_distinct": true}
			for k, v := range purchase {
				distinct[k] = v
			}
			got = rudolfo.interpret(notification.ObservationID, distinct)
		}
		if got.Effect != "created" {
			t.Fatalf("notification = %+v, want a purchase of its own", got)
		}
		ids = append(ids, got.Transaction.ID)
	}
	merge, refused := callTool[mcpserver.ConfirmMatchOutput](t, rudolfo.session, "confirm_match", map[string]any{
		"transaction_id": ids[0], "other_transaction_id": ids[1], "request_id": requestID(), "reason": "parecem a mesma compra",
	})
	vague := map[string]any{"raw_text": "Gastei 32,97 na padaria dia 2", "amount": "32.97", "description": "Padaria", "occurred_on": "2026-10-02", "category": "meals", "category_status": "suggested"}
	id := requestID()
	ambiguous := rudolfo.record(id, vague)
	vague["defer_match"] = true
	deferred := rudolfo.record(id, vague)

	if len(ids) != 2 || ids[0] == ids[1] {
		t.Fatalf("notified purchases = %v, want two", ids)
	}
	if refused == nil {
		t.Errorf("confirm_match of two notified operations = %+v, want it refused", merge)
	}
	if ambiguous.Result != "possible_match" || len(ambiguous.Candidates) != 2 {
		t.Errorf("conversation = %+v, want both purchases as candidates", ambiguous)
	}
	if deferred.Result != "recorded" || deferred.Transaction.Status != "pending_clarification" {
		t.Errorf("deferred = %+v, want it left for review", deferred)
	}
	if got := rudolfo.transactions(); len(got) != 3 {
		t.Errorf("transactions = %d, want both purchases and the one under review", len(got))
	}
}

func TestCriterion05InstallmentPurchase(t *testing.T) {
	rudolfo := newSuite(t).signUp("Rudolfo")
	tv := rudolfo.record(requestID(), map[string]any{
		"raw_text": "Comprei uma TV de 1.200 em 12x de 100 no crédito final 1234", "amount": "1200", "description": "TV", "merchant": "Loja de TV",
		"occurred_on": "2026-09-10", "payment_method": "credit_card", "card_last4": "1234", "installment_count": 12, "installment_amount": "100", "category": "home", "category_status": "suggested",
	})
	first := line("installment", "LOJA DE TV 01/12", "100.00", "2026-09-10", map[string]any{"installment_number": 1, "installment_count": 12, "category": "home"})
	reading := statement("1234", "2026-10-03", "100.00", first)
	if asked := rudolfo.submitStatement(reading); asked.Result == "possible_match" {
		first["related_transaction_id"] = tv.Transaction.ID
	}
	submitted := rudolfo.submitStatement(reading)

	purchases := use[mcpserver.QuerySpendingOutput](rudolfo, "query_spending", map[string]any{"from": "2026-09-01", "to": "2026-09-30"})
	charged := use[mcpserver.StatementSummaryOutput](rudolfo, "get_statement_summary", map[string]any{})
	future := use[mcpserver.FutureCommitmentsOutput](rudolfo, "get_future_commitments", map[string]any{})

	if submitted.Lines[0].TransactionID == nil || *submitted.Lines[0].TransactionID != tv.Transaction.ID {
		t.Fatalf("installment line = %+v, want it on the TV purchase", submitted.Lines)
	}
	if total := purchases.Totals[0]; !sameMoney(total.Confirmed.Amount, "1200.00") && !sameMoney(total.Provisional.Amount, "1200.00") {
		t.Errorf("purchases = %+v, want R$ 1.200,00", purchases.Totals)
	}
	if !sameMoney(charged.Review.Composition.ComputedTotal, "100.00") {
		t.Errorf("statement = %+v, want R$ 100,00", charged.Review.Composition)
	}
	if len(future.Totals) != 1 || !sameMoney(future.Totals[0].Total, "1100.00") {
		t.Errorf("future commitments = %+v, want R$ 1.100,00", future.Totals)
	}
}

func TestCriterion06PixWithInterest(t *testing.T) {
	rudolfo := newSuite(t).signUp("Rudolfo")
	rudolfo.submitStatement(statement("1234", "2026-10-03", "500.00",
		line("pix_installment", "PIX PARCELADO MARIA 02/06", "500.00", "2026-08-10", map[string]any{
			"installment_number": 2, "installment_count": 6, "purpose": "reforma", "category": "home",
			"components": []any{
				map[string]any{"kind": "principal", "amount": "450.00", "included": true},
				map[string]any{"kind": "interest", "amount": "50.00", "included": true},
			},
		})))

	pix := use[mcpserver.PixInstallmentSummaryOutput](rudolfo, "get_pix_installment_summary", map[string]any{})
	review := use[mcpserver.GetStatementReviewOutput](rudolfo, "get_statement_review", map[string]any{})

	breakdown := map[string]string{}
	for _, component := range pix.Totals[0].Breakdown {
		breakdown[component.Kind] = component.Amount.Amount
	}
	if !sameMoney(pix.Totals[0].Charged, "500.00") || breakdown["principal"] != "450.00" || breakdown["interest"] != "50.00" {
		t.Errorf("Pix parcelado = %+v, want R$ 450,00 of principal and R$ 50,00 of interest inside R$ 500,00", pix.Totals)
	}
	if !sameMoney(review.Review.Composition.ComputedTotal, "500.00") || review.Review.Status != "reconciled" {
		t.Errorf("statement = %+v, want the interest counted once", review.Review)
	}
}

func TestCriterion07UnknownPurpose(t *testing.T) {
	rudolfo := newSuite(t).signUp("Rudolfo")
	rudolfo.submitStatement(statement("1234", "2026-10-03", "300.00",
		line("pix_installment", "PIX PARCELADO JOSE 01/03", "300.00", "2026-09-05", map[string]any{"installment_number": 1, "installment_count": 3})))

	before := use[mcpserver.StatementSummaryOutput](rudolfo, "get_statement_summary", map[string]any{})
	questions := rudolfo.clarifications("purpose_unknown")
	if len(questions) != 1 {
		t.Fatalf("purpose questions = %+v, want the Pix", questions)
	}
	use[mcpserver.ResolveClarificationOutput](rudolfo, "resolve_clarification", map[string]any{
		"clarification_id": questions[0].ID, "request_id": requestID(), "action": "resolve", "note": "foi o empréstimo para a reforma", "purpose": "reforma",
	})
	after := use[mcpserver.StatementSummaryOutput](rudolfo, "get_statement_summary", map[string]any{})

	if !sameMoney(before.Review.Composition.ComputedTotal, "300.00") || !sameMoney(after.Review.Composition.ComputedTotal, "300.00") {
		t.Errorf("charged before and after = %s / %s, want R$ 300,00 both times", before.Review.Composition.ComputedTotal.Amount, after.Review.Composition.ComputedTotal.Amount)
	}
	if len(rudolfo.clarifications("purpose_unknown")) != 0 {
		t.Error("the answered question is still open")
	}
}

func TestCriterion08ConvenienceSnacks(t *testing.T) {
	rudolfo := newSuite(t).signUp("Rudolfo")
	categories := use[mcpserver.ListCategoriesOutput](rudolfo, "list_categories", map[string]any{})
	codes := make([]string, len(categories.Categories))
	for i, category := range categories.Categories {
		codes[i] = category.Code
	}
	for _, code := range []string{"convenience_snacks", "meals", "groceries", "fuel"} {
		if !slices.Contains(codes, code) {
			t.Fatalf("categories = %v, want %s apart", codes, code)
		}
	}

	right := rudolfo.evaluate("convenience_snacks")
	wrong := rudolfo.evaluate("meals")

	if !right.Report.Criterion8 {
		t.Errorf("evaluation with convenience = %+v, want criterion 8 met", right.Report)
	}
	if wrong.Report.Criterion8 {
		t.Errorf("evaluation with snacks as a meal = %+v, want criterion 8 failed", wrong.Report)
	}
}

// evaluate runs a Case evaluation where the snacks at the gas station's
// convenience store are classified as category, and fuel as fuel.
func (p *person) evaluate(category string) mcpserver.FinishCaseEvaluationOutput {
	p.s.t.Helper()
	started := use[mcpserver.StartCaseEvaluationOutput](p, "start_case_evaluation", map[string]any{})
	text := map[string]string{}
	for _, message := range started.Conversation {
		text[message.ID] = message.Text
	}
	p.record(requestID(), map[string]any{"raw_text": text["c2"], "amount": "18.50", "description": "Refrigerante e chocolate", "date_reference": "now", "payment_method": "debit_card", "category": category, "category_status": "suggested"})
	p.record(requestID(), map[string]any{"raw_text": text["c3"], "amount": "200", "description": "Gasolina", "date_reference": "now", "payment_method": "credit_card", "category": "fuel", "category_status": "suggested"})
	return use[mcpserver.FinishCaseEvaluationOutput](p, "finish_case_evaluation", map[string]any{})
}

func TestCriterion09RepeatedStatement(t *testing.T) {
	rudolfo := newSuite(t).signUp("Rudolfo")
	reading := func(uber string, total string) map[string]any {
		return statement("1234", "2026-10-03", total,
			line("purchase", "SUPERMERCADO EXEMPLO", "152.30", "2026-09-14", map[string]any{"description": "Supermercado", "category": "groceries"}),
			line("purchase", "UBER *TRIP", uber, "2026-09-22", map[string]any{"description": "Uber", "category": "transport"}))
	}

	first := rudolfo.submitStatement(reading("15.90", "168.20"))
	again := rudolfo.submitStatement(reading("15.90", "168.20"))
	count := len(rudolfo.transactions())
	changed := rudolfo.submitStatement(reading("19.90", "172.20"))

	if first.Result != "recorded" || again.Result != "already_recorded" || count != 2 {
		t.Fatalf("results = %s then %s with %d transactions, want the second one to change nothing", first.Result, again.Result, count)
	}
	if changed.Result != "new_version" || changed.Diff == nil || len(changed.Diff.Changed) != 1 || len(changed.Diff.Metadata) == 0 {
		t.Errorf("changed reading = %+v, want the different line and total highlighted", changed)
	}
	if got := len(rudolfo.transactions()); got != 2 {
		t.Errorf("transactions after the new version = %d, want 2", got)
	}
}

func TestCriterion10PartialRefund(t *testing.T) {
	rudolfo := newSuite(t).signUp("Rudolfo")
	purchase := rudolfo.record(requestID(), map[string]any{"raw_text": "Comprei uma cadeira de 100 no débito", "amount": "100", "description": "Cadeira", "merchant": "Loja Exemplo", "occurred_on": "2026-10-10", "payment_method": "debit_card", "category": "home", "category_status": "suggested"})
	refund := rudolfo.record(requestID(), map[string]any{"raw_text": "A loja devolveu 30 da cadeira", "amount": "30", "description": "Estorno da cadeira", "nature": "refund", "refund_of_id": purchase.Transaction.ID, "occurred_on": "2026-10-15", "payment_method": "debit_card"})

	detail := use[mcpserver.TransactionDetailOutput](rudolfo, "get_transaction", map[string]any{"transaction_id": purchase.Transaction.ID})
	october := use[mcpserver.QuerySpendingOutput](rudolfo, "query_spending", map[string]any{"from": "2026-10-01", "to": "2026-10-21"})

	if refund.Result != "recorded" || len(rudolfo.transactions()) != 2 {
		t.Fatalf("refund = %+v, want both records kept", refund)
	}
	if detail.Net == nil || !sameMoney(*detail.Net, "70.00") || len(detail.Refunds) != 1 {
		t.Errorf("purchase = %+v, want R$ 70,00 net", detail)
	}
	if total := october.Totals[0]; !sameMoney(total.NetConfirmed, "70.00") || !sameMoney(total.Refunds.Amount, "30.00") {
		t.Errorf("October = %+v, want R$ 70,00 net", october.Totals)
	}
}

func TestCriterion11IncompleteCoverage(t *testing.T) {
	rudolfo := newSuite(t).signUp("Rudolfo")
	rudolfo.track(saoPaulo("2026-10-10T00:00:00-03:00"))

	before := use[mcpserver.QuerySpendingOutput](rudolfo, "query_spending", map[string]any{"from": "2026-09-01", "to": "2026-09-30"})
	coverage := use[mcpserver.GetCoverageStatusOutput](rudolfo, "get_coverage_status", map[string]any{"from": "2026-09-01", "to": "2026-09-30"})

	if before.Coverage.Complete || len(before.Coverage.Limitations) == 0 || !strings.Contains(before.Summary, "não significa que não houve gastos") {
		t.Errorf("spending before the tracking = %+v, want the limitation instead of no spending", before)
	}
	if len(coverage.Limitations) == 0 {
		t.Errorf("coverage = %+v, want the limitations to relay", coverage)
	}
}

func TestCriterion12ReconciledStatement(t *testing.T) {
	rudolfo := newSuite(t).signUp("Rudolfo")
	items := func() []map[string]any {
		return []map[string]any{
			line("purchase", "SUPERMERCADO EXEMPLO", "152.30", "2026-09-14", map[string]any{"description": "Supermercado", "category": "groceries"}),
			line("tax", "IOF TRANSACAO EXTERIOR", "3.85", "2026-09-20", map[string]any{"category": "financial_charges"}),
			line("refund", "ESTORNO LOJA EXEMPLO", "30.00", "2026-09-25", nil),
		}
	}
	explained := statement("1111", "2026-10-03", "126.15", items()...)
	divergent := statement("2222", "2026-10-03", "130.00", items()...)
	partial := statement("3333", "2026-10-03", "126.15", items()...)
	partial["reading"], partial["missing"] = "partial", "a página 2 estava ilegível"

	got := map[string]*mcpserver.StatementReviewOutput{}
	for card, reading := range map[string]map[string]any{"1111": explained, "2222": divergent, "3333": partial} {
		got[card] = rudolfo.submitStatement(reading).Review
	}

	if got["1111"].Status != "reconciled" || !sameMoney(got["1111"].Composition.ComputedTotal, "126.15") || !sameMoney(got["1111"].Composition.Difference, "0.00") {
		t.Errorf("explained statement = %+v, want it reconciled", got["1111"])
	}
	if got["2222"].Status != "divergent" || !sameMoney(got["2222"].Composition.Difference, "3.85") {
		t.Errorf("statement with a difference = %+v, want it divergent", got["2222"])
	}
	if got["3333"].Status != "read_incomplete" {
		t.Errorf("partial reading = %+v, want it incomplete", got["3333"])
	}
}

func TestCriterion13CorrectionPreserved(t *testing.T) {
	rudolfo := newSuite(t).signUp("Rudolfo")
	purchase := rudolfo.record(requestID(), map[string]any{
		"raw_text": "Gastei 45,90 na padaria dia 2 no crédito final 1234", "amount": "45.90", "description": "Padaria", "merchant": "Padaria Pão Quente",
		"occurred_on": "2026-10-02", "payment_method": "credit_card", "card_last4": "1234", "category": "meals", "category_status": "suggested",
	})
	use[mcpserver.CorrectionOutput](rudolfo, "set_category", map[string]any{"transaction_id": purchase.Transaction.ID, "request_id": requestID(), "category": "groceries", "category_status": "confirmed", "reason": "Rudolfo disse que foi compra de mercado"})
	bakery := line("purchase", "PADARIA PAO QUENTE", "45.90", "2026-10-02", map[string]any{"description": "Padaria", "category": "meals", "related_transaction_id": purchase.Transaction.ID})

	submitted := rudolfo.submitStatement(statement("1234", "2026-10-03", "45.90", bakery))
	afterStatement := use[mcpserver.TransactionDetailOutput](rudolfo, "get_transaction", map[string]any{"transaction_id": purchase.Transaction.ID})
	reviewed := use[mcpserver.CorrectionOutput](rudolfo, "set_category", map[string]any{"transaction_id": purchase.Transaction.ID, "request_id": requestID(), "category": "meals", "category_status": "suggested", "reason": "Case conferiu a compra de novo"})
	detail := use[mcpserver.TransactionDetailOutput](rudolfo, "get_transaction", map[string]any{"transaction_id": purchase.Transaction.ID})

	if submitted.Lines[0].Effect != "linked" {
		t.Fatalf("statement = %+v", submitted.Lines)
	}
	for when, category := range map[string]*mcpserver.CategoryAllocationOutput{"statement": afterStatement.Transaction.Category, "review": detail.Transaction.Category} {
		if category == nil || category.Code != "groceries" || category.Status != "confirmed" {
			t.Errorf("category after the %s = %+v, want Rudolfo's groceries kept", when, category)
		}
	}
	if !slices.ContainsFunc(detail.Clarifications, func(c mcpserver.ClarificationOutput) bool { return c.Kind == "field_conflict" }) {
		t.Errorf("clarifications = %+v (%s), want Case's different reading asked, never applied", detail.Clarifications, reviewed.Summary)
	}
}

func TestCriterion14Privacy(t *testing.T) {
	s := newSuite(t)
	rudolfo, other := s.signUp("Rudolfo"), s.signUp("Outra pessoa")
	postedAt := saoPaulo("2026-10-21T15:04:05-03:00")
	rudolfo.track(postedAt.Add(-24 * time.Hour))
	purchase := rudolfo.record(requestID(), map[string]any{"raw_text": "Gastei 327,39 no mercado", "amount": "327.39", "description": "Mercado"})
	rudolfo.notify("0|wallet|9", "posted", "Compra de R$ 99,00 com cartão 5555 5555 5555 4444, código: 482913", postedAt)

	_, hidden := callTool[mcpserver.TransactionDetailOutput](t, other.session, "get_transaction", map[string]any{"transaction_id": purchase.Transaction.ID})
	listed := use[mcpserver.ListTransactionsOutput](other, "list_transactions", map[string]any{})
	_, phone := callTool[mcpserver.ListTransactionsOutput](t, s.connect(rudolfo.phone), "list_transactions", map[string]any{})
	text := rudolfo.pending().Notifications[0].Text

	if hidden == nil || hidden.Code != "not_found" || len(listed.Transactions) != 0 {
		t.Errorf("another account sees %d transactions and get_transaction = %+v, want nothing of Rudolfo's", len(listed.Transactions), hidden)
	}
	if phone == nil || phone.Code != "forbidden" {
		t.Errorf("the phone's key reading transactions = %+v, want forbidden", phone)
	}
	if strings.Contains(text, "5555 5555 5555 4444") || strings.Contains(text, "482913") || !strings.Contains(text, "4444") {
		t.Errorf("stored notification = %q, want the card masked and the code removed", text)
	}
	logs := s.logs.String()
	for _, secret := range []string{rudolfo.admin, rudolfo.caseKey, rudolfo.phone, "482913", "5555 5555 5555 4444"} {
		if strings.Contains(logs, secret) {
			t.Errorf("logs carry a secret: %q", secret)
		}
	}
	if status := s.request(http.MethodGet, "/v1/api-keys", other.caseKey, nil); status != http.StatusForbidden {
		t.Errorf("Case's key on an admin route = %d, want 403", status)
	}
}

func TestEveryCriterionHasATest(t *testing.T) {
	tests := []func(*testing.T){
		TestCriterion01RecordingByConversation, TestCriterion02RepeatedNotification, TestCriterion03SamePurchaseFromThreeSources,
		TestCriterion04LegitimateEqualPurchases, TestCriterion05InstallmentPurchase, TestCriterion06PixWithInterest,
		TestCriterion07UnknownPurpose, TestCriterion08ConvenienceSnacks, TestCriterion09RepeatedStatement,
		TestCriterion10PartialRefund, TestCriterion11IncompleteCoverage, TestCriterion12ReconciledStatement,
		TestCriterion13CorrectionPreserved, TestCriterion14Privacy,
	}
	if len(tests) != len(acceptance.Criteria) {
		t.Errorf("%d criterion tests for %d criteria", len(tests), len(acceptance.Criteria))
	}
}
