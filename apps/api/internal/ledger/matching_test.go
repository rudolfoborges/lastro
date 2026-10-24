package ledger_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rudolfoborges/lastro/apps/api/internal/ledger"
	"github.com/rudolfoborges/lastro/apps/api/internal/money"
)

func bakeryStatement() ledger.StatementExtraction {
	purchase := line(ledger.ItemPurchase, "PADARIA PAO QUENTE LTDA", 3297, "2026-10-23", 1)
	purchase.Merchant, purchase.Category = ptr("PADARIA PAO QUENTE LTDA"), ptr("meals")
	return ledger.StatementExtraction{
		Issuer:      "Banco Exemplo",
		CardLast4:   "1234",
		ClosingDate: *day("2026-11-03"),
		DueDate:     *day("2026-11-10"),
		PeriodStart: day("2026-10-04"),
		PeriodEnd:   day("2026-11-03"),
		Total:       brl(3297),
		Reading:     ledger.ReadingComplete,
		PageCount:   ptr(1),
		Items:       []ledger.StatementItem{purchase},
	}
}

func bakeryReport(requestID string) ledger.ExpenseReport {
	report := supermarket(requestID)
	report.ReceivedAt = postedAt.Add(-time.Hour)
	report.RawText, report.Description = "Paguei 32,97 na padaria", "Café na padaria"
	report.Amount = ptr(brl(3297))
	report.Merchant = ptr("Padaria Pão Quente")
	report.PaymentMethod = &ledger.PaymentMethodRef{Kind: ledger.PaymentCreditCard}
	report.Category = ptr("meals")
	return report
}

func (f *fixture) findMatches(ref ledger.MatchRef) ledger.MatchSearch {
	f.t.Helper()
	search, err := f.ledger.FindMatches(f.t.Context(), f.account, ref)
	if err != nil {
		f.t.Fatalf("FindMatches() error = %v", err)
	}
	return search
}

func hasText(values []string, fragment string) bool {
	return slices.ContainsFunc(values, func(v string) bool { return strings.Contains(v, fragment) })
}

func TestAcceptanceCriterion3TheSamePurchaseFromThreeSourcesIsOneTransaction(t *testing.T) {
	f := newFixture(t)
	told := f.record(bakeryReport("message-1")).Transaction
	observation := f.walletObservation(walletPurchase("0|wallet|7|null|10123", "R$ 32,97 com Visa •••• 1234"))
	notified := f.interpret(purchaseReading(observation, brl(3297)))
	reading := purchaseReading(observation, brl(3297))
	reading.RelatedTransactionID = &told.ID
	f.interpret(reading)

	statement := bakeryStatement()
	offered := f.submitStatement(statement)
	statement.Items[0].RelatedTransactionID = &told.ID
	charged := f.submitStatement(statement)

	if notified.Result != ledger.InterpretationPossibleMatch {
		t.Errorf("notification result = %s, want possible_match against the conversation record", notified.Result)
	}
	if offered.Result != ledger.StatementPossibleMatch || offered.Candidates[0].Candidates[0].Transaction.ID != told.ID {
		t.Fatalf("statement = %+v, want the record offered for the line", offered)
	}
	if sources := offered.Candidates[0].Candidates[0].Sources; !slices.Equal(sources, []ledger.Source{ledger.SourceConversation, ledger.SourceWalletNotification}) {
		t.Errorf("candidate sources = %v, want conversation and wallet_notification", sources)
	}
	if charged.Lines[0].Effect != ledger.EffectLinked {
		t.Errorf("line effect = %s, want linked", charged.Lines[0].Effect)
	}
	detail, err := f.ledger.GetTransaction(t.Context(), f.account, told.ID)
	if err != nil {
		t.Fatal(err)
	}
	sources := map[ledger.Source]int{}
	for _, evidence := range detail.Evidence {
		if evidence.Status == ledger.EvidenceConfirmed {
			sources[evidence.Source]++
		}
	}
	if len(sources) != 3 || sources[ledger.SourceConversation] != 1 || sources[ledger.SourceWalletNotification] != 1 || sources[ledger.SourceStatementLine] != 1 {
		t.Errorf("confirmed evidence by source = %v, want one of each", sources)
	}
	if f.count("transactions") != 1 || detail.Amount != brl(3297) || detail.Status != ledger.StatusConfirmed {
		t.Errorf("transactions = %d, detail = %+v, want one confirmed transaction of R$ 32,97", f.count("transactions"), detail.Transaction)
	}
}

func TestAcceptanceCriterion4EqualPurchasesFromTwoNotificationsStayApart(t *testing.T) {
	f := newFixture(t)
	first := f.walletObservation(walletPurchase("0|wallet|1|null|10123", "R$ 32,97"))
	one := f.interpret(purchaseReading(first, brl(3297))).Transaction
	second := f.walletObservation(walletPurchase("0|wallet|2|null|10123", "R$ 32,97"))
	two := f.interpret(purchaseReading(second, brl(3297)))

	report := bakeryReport("message-1")
	report.ReceivedAt = postedAt.Add(time.Hour)
	told := f.record(report)
	search := f.findMatches(ledger.MatchRef{TransactionID: &one.ID})

	assertInterpretation(t, two, ledger.InterpretationApplied, ledger.EffectCreated)
	if told.Result != ledger.ExpensePossibleMatch || len(told.Candidates) != 2 {
		t.Fatalf("conversation = %s with %d candidates, want both notifications offered", told.Result, len(told.Candidates))
	}
	for _, candidate := range told.Candidates {
		if !hasText(candidate.Reasons, "mesmo valor") || !hasText(candidate.Reasons, "mesmo estabelecimento") {
			t.Errorf("candidate reasons = %v, want the amount and the merchant explained", candidate.Reasons)
		}
	}
	if len(search.Candidates) != 0 {
		t.Errorf("candidates of a notified purchase = %+v, want none from another notification", search.Candidates)
	}
	link := purchaseReading(second, brl(3297))
	link.RelatedTransactionID = &one.ID
	link.Reinterpret, link.Reason = true, "Mesma compra"
	if _, err := f.ledger.InterpretNotification(t.Context(), f.account, link, agent); err == nil {
		t.Error("a second notification confirmed the purchase of the first")
	}
	if got := f.count("transactions"); got != 2 {
		t.Errorf("transactions = %d, want the two legitimate purchases", got)
	}
}

func TestTheConversationConfirmsAPurchaseGoogleWalletAlreadyRecorded(t *testing.T) {
	f := newFixture(t)
	observation := f.walletObservation(walletPurchase("0|wallet|7|null|10123", "R$ 32,97 com Visa •••• 1234"))
	notified := f.interpret(purchaseReading(observation, brl(3297))).Transaction
	report := bakeryReport("message-1")
	report.ReceivedAt = postedAt.Add(2 * time.Hour)
	report.Purpose = ptr("café da equipe")
	report.CategoryStatus = ledger.AllocationConfirmed

	offered := f.record(report)
	report.RelatedTransactionID = &notified.ID
	linked := f.record(report)
	repeated := f.record(report)

	if offered.Result != ledger.ExpensePossibleMatch || len(offered.Candidates) != 1 || offered.Candidates[0].Transaction.ID != notified.ID {
		t.Fatalf("offered = %+v, want the notified purchase as candidate", offered)
	}
	if !slices.Equal(offered.Candidates[0].Sources, []ledger.Source{ledger.SourceWalletNotification}) {
		t.Errorf("sources = %v, want wallet_notification", offered.Candidates[0].Sources)
	}
	if linked.Result != ledger.ExpenseLinked || linked.Transaction.ID != notified.ID {
		t.Fatalf("linked = %s %+v, want the notified purchase", linked.Result, linked.Transaction)
	}
	if linked.Transaction.Purpose == nil || *linked.Transaction.Purpose != "café da equipe" || !slices.Contains(linked.Changed, ledger.FieldPurpose) {
		t.Errorf("linked = %+v changed %v, want the purpose filled from the conversation", linked.Transaction, linked.Changed)
	}
	if linked.Category.Code != "meals" || linked.Category.Status != ledger.AllocationConfirmed {
		t.Errorf("category = %+v, want meals confirmed by Rudolfo", linked.Category)
	}
	if linked.Transaction.Description != notified.Description || linked.Transaction.Amount != notified.Amount {
		t.Errorf("linked = %+v, want description and amount of the notification kept", linked.Transaction)
	}
	if repeated.Result != ledger.ExpenseDuplicateOf || f.count("transactions") != 1 || f.count("observations") != 2 {
		t.Errorf("repeated = %s, transactions = %d, observations = %d", repeated.Result, f.count("transactions"), f.count("observations"))
	}
}

func TestASecondConversationReportCannotConfirmTheSamePurchase(t *testing.T) {
	f := newFixture(t)
	told := f.record(supermarket("message-1")).Transaction
	report := supermarket("message-2")
	report.Conversation, report.RelatedTransactionID = "conversation-2", &told.ID

	_, err := f.ledger.RecordExpense(t.Context(), f.account, report, agent)

	assertValidation(t, err, "related_transaction_id")
	if f.count("transactions") != 1 || f.count("observations") != 1 {
		t.Errorf("transactions = %d, observations = %d, want nothing new", f.count("transactions"), f.count("observations"))
	}
}

func TestAnotherPurchaseOfTheSameAmountIsRecordedWhenCaseConfirmsIt(t *testing.T) {
	f := newFixture(t)
	f.submitStatement(bakeryStatement())
	report := bakeryReport("message-1")
	report.ConfirmDistinct = true

	recorded := f.record(report)

	if recorded.Result != ledger.ExpenseRecorded || f.count("transactions") != 2 {
		t.Errorf("result = %s, transactions = %d, want a second purchase", recorded.Result, f.count("transactions"))
	}
}

func TestFindingMatchesExplainsReasonsAndDifferences(t *testing.T) {
	f := newFixture(t)
	charged := f.submitStatement(bakeryStatement()).Lines[0]
	report := bakeryReport("message-1")
	report.DateReference, report.OccurredOn = nil, day("2026-10-19")
	told := f.record(report).Transaction

	byTransaction := f.findMatches(ledger.MatchRef{TransactionID: &told.ID})
	byLine := f.findMatches(ledger.MatchRef{ObservationID: &charged.ObservationID})

	if len(byTransaction.Candidates) != 1 || byTransaction.Candidates[0].Transaction.ID != charged.Transaction.ID {
		t.Fatalf("candidates = %+v, want the statement purchase", byTransaction.Candidates)
	}
	candidate := byTransaction.Candidates[0]
	if !hasText(candidate.Reasons, "mesmo valor") || !hasText(candidate.Reasons, "mesmo meio de pagamento") {
		t.Errorf("reasons = %v, want the amount and the payment method", candidate.Reasons)
	}
	if !hasText(candidate.Differences, "nome diferente") || !hasText(candidate.Differences, "4 dias antes") {
		t.Errorf("differences = %v, want the name and the day apart", candidate.Differences)
	}
	if byLine.Transaction.ID != charged.Transaction.ID || len(byLine.Candidates) != 1 || byLine.Candidates[0].Transaction.ID != told.ID {
		t.Errorf("by line = %+v, want the conversation record", byLine)
	}
	pending := f.walletObservation(walletPurchase("0|wallet|7|null|10123", "R$ 32,97"))
	_, err := f.ledger.FindMatches(t.Context(), f.account, ledger.MatchRef{ObservationID: &pending})
	assertValidation(t, err, "observation_id")
}

func TestInternationalPurchasesMatchByTheOriginalAmount(t *testing.T) {
	f := newFixture(t)
	observation := f.walletObservation(walletPurchase("0|wallet|7|null|10123", "US$ 20,00"))
	reading := purchaseReading(observation, money.Money{Minor: 2000, Currency: "USD"})
	reading.Merchant = ptr("Example Software")
	notified := f.interpret(reading).Transaction
	statement := bakeryStatement()
	statement.Items[0] = line(ledger.ItemPurchase, "EXAMPLE SOFTWARE", 11000, "2026-10-22", 1)
	statement.Items[0].OriginalAmount = &money.Money{Minor: 2000, Currency: "USD"}
	statement.Total = brl(11000)

	offered := f.submitStatement(statement)

	if offered.Result != ledger.StatementPossibleMatch || offered.Candidates[0].Candidates[0].Transaction.ID != notified.ID {
		t.Fatalf("statement = %+v, want the notified purchase in dollars offered", offered)
	}
	if reasons := offered.Candidates[0].Candidates[0].Reasons; !hasText(reasons, "moeda original") {
		t.Errorf("reasons = %v, want the original amount explained", reasons)
	}
}

func TestTheReviewListsCardPurchasesTheStatementDidNotConfirm(t *testing.T) {
	f := newFixture(t)
	missing := bakeryReport("message-1")
	missing.RawText, missing.Description, missing.Merchant = "Gastei 50 na farmácia", "Farmácia", nil
	missing.Amount, missing.DateReference, missing.OccurredOn = ptr(brl(5000)), nil, day("2026-10-20")
	unmatched := f.record(missing).Transaction
	debit := bakeryReport("message-2")
	debit.RawText, debit.Description, debit.Merchant = "Gastei 40 no débito", "Farmácia", nil
	debit.Amount, debit.DateReference, debit.OccurredOn = ptr(brl(4000)), nil, day("2026-10-20")
	debit.PaymentMethod = &ledger.PaymentMethodRef{Kind: ledger.PaymentDebitCard}
	f.record(debit)

	out := f.submitStatement(bakeryStatement())

	if out.Review == nil || len(out.Review.Unmatched) != 1 || out.Review.Unmatched[0].ID != unmatched.ID {
		t.Fatalf("review = %+v, want only the credit card purchase missing from the statement", out.Review)
	}
	if out.Review.Status != ledger.ReviewReconciled {
		t.Errorf("status = %s, want reconciled: a purchase missing from the statement does not change its total", out.Review.Status)
	}
	if !slices.ContainsFunc(out.Review.Pending, func(p ledger.ReviewPending) bool { return p.Kind == ledger.PendingUnmatched }) {
		t.Errorf("pending = %+v, want the unmatched purchases listed", out.Review.Pending)
	}
}
