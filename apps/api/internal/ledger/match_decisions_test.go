package ledger_test

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rudolfoborges/lastro/apps/api/internal/calendar"
	"github.com/rudolfoborges/lastro/apps/api/internal/ledger"
)

func (f *fixture) confirmMatch(a, b uuid.UUID) ledger.MergeOutcome {
	f.t.Helper()
	outcome, err := f.ledger.ConfirmMatch(f.t.Context(), f.account, ledger.MatchDecision{TransactionID: a, OtherTransactionID: b, Reason: "Rudolfo disse que é a mesma compra"}, agent)
	if err != nil {
		f.t.Fatalf("ConfirmMatch() error = %v", err)
	}
	return outcome
}

func (f *fixture) undoMatch(transactionID, observationID uuid.UUID) ledger.UndoOutcome {
	f.t.Helper()
	outcome, err := f.ledger.UndoMatch(f.t.Context(), f.account, ledger.UndoRequest{TransactionID: transactionID, ObservationID: observationID, Reason: "Vínculo errado"}, agent)
	if err != nil {
		f.t.Fatalf("UndoMatch() error = %v", err)
	}
	return outcome
}

func (f *fixture) detail(id uuid.UUID) ledger.TransactionDetail {
	f.t.Helper()
	detail, err := f.ledger.GetTransaction(f.t.Context(), f.account, id)
	if err != nil {
		f.t.Fatal(err)
	}
	return detail
}

func confirmedSources(detail ledger.TransactionDetail) map[ledger.Source]uuid.UUID {
	sources := map[ledger.Source]uuid.UUID{}
	for _, evidence := range detail.Evidence {
		if evidence.Status == ledger.EvidenceConfirmed {
			sources[evidence.Source] = evidence.ObservationID
		}
	}
	return sources
}

// roundedReport is a purchase Rudolfo told with a rounded amount, so no
// candidate was offered and two transactions exist for the same purchase.
func (f *fixture) roundedReport() ledger.Transaction {
	f.t.Helper()
	report := bakeryReport("message-1")
	report.Amount = ptr(brl(3300))
	report.Purpose = ptr("café da equipe")
	report.Category, report.CategoryStatus = ptr("groceries"), ledger.AllocationConfirmed
	return *f.record(report).Transaction
}

func (f *fixture) notifiedBakery(key string) ledger.Transaction {
	f.t.Helper()
	observation := f.walletObservation(walletPurchase(key, "R$ 32,97 com Visa •••• 1234"))
	return *f.interpret(purchaseReading(observation, brl(3297))).Transaction
}

func TestConfirmingAMatchMergesTwoRecordsOfTheSamePurchase(t *testing.T) {
	f := newFixture(t)
	told := f.roundedReport()
	notified := f.notifiedBakery("0|wallet|7|null|10123")

	merged := f.confirmMatch(told.ID, notified.ID)

	if merged.Survivor.ID != notified.ID || merged.Merged.ID != told.ID {
		t.Fatalf("survivor = %s, merged = %s, want the notification to hold the purchase", merged.Survivor.ID, merged.Merged.ID)
	}
	if merged.Merged.Status != ledger.StatusMerged || merged.Merged.MergedIntoID == nil || *merged.Merged.MergedIntoID != notified.ID {
		t.Errorf("merged = %+v, want status merged into the notification", merged.Merged)
	}
	if merged.Survivor.Amount != brl(3297) || merged.Survivor.Purpose == nil || *merged.Survivor.Purpose != "café da equipe" {
		t.Errorf("survivor = %+v, want the notified amount kept and the purpose filled", merged.Survivor)
	}
	if merged.Category.Code != "groceries" || merged.Category.Status != ledger.AllocationConfirmed {
		t.Errorf("category = %+v, want the one Rudolfo confirmed", merged.Category)
	}
	sources := confirmedSources(f.detail(notified.ID))
	if len(sources) != 2 || sources[ledger.SourceConversation] == uuid.Nil {
		t.Errorf("evidence = %v, want the conversation moved to the survivor", sources)
	}
	if len(confirmedSources(f.detail(told.ID))) != 0 {
		t.Error("the merged transaction kept confirmed evidence")
	}
}

func TestAcceptanceCriterion4TwoNotifiedPurchasesAreNeverMerged(t *testing.T) {
	f := newFixture(t)
	one := f.notifiedBakery("0|wallet|1|null|10123")
	two := f.notifiedBakery("0|wallet|2|null|10123")

	_, err := f.ledger.ConfirmMatch(t.Context(), f.account, ledger.MatchDecision{TransactionID: one.ID, OtherTransactionID: two.ID, Reason: "Parece igual"}, agent)

	assertValidation(t, err, "other_transaction_id")
	if f.detail(two.ID).Status != ledger.StatusProvisional {
		t.Error("a refused merge changed a transaction")
	}
}

func TestUndoingAMergeBringsBothTransactionsBackAsTheyWere(t *testing.T) {
	f := newFixture(t)
	told := f.roundedReport()
	notified := f.notifiedBakery("0|wallet|7|null|10123")
	conversation := confirmedSources(f.detail(told.ID))[ledger.SourceConversation]
	f.confirmMatch(told.ID, notified.ID)

	undone := f.undoMatch(notified.ID, conversation)

	if undone.Effect != ledger.UndoUnmerged || len(undone.Transactions) != 2 {
		t.Fatalf("undo = %+v, want both transactions back", undone)
	}
	restored := f.detail(told.ID)
	if restored.Status != ledger.StatusProvisional || restored.MergedIntoID != nil || restored.Amount != brl(3300) {
		t.Errorf("restored = %+v, want the conversation record as it was", restored.Transaction)
	}
	if sources := confirmedSources(restored); sources[ledger.SourceConversation] != conversation {
		t.Errorf("restored evidence = %v, want its conversation back", sources)
	}
	survivor := f.detail(notified.ID)
	if survivor.Purpose != nil || survivor.Category.Code != "meals" || survivor.Category.Status != ledger.AllocationSuggested {
		t.Errorf("survivor = %+v %+v, want the purpose and category it had before the merge", survivor.Transaction, survivor.Category)
	}
	if search := f.findMatches(ledger.MatchRef{TransactionID: &told.ID}); len(search.Candidates) != 0 {
		t.Errorf("candidates = %+v, want the undone pair never offered again", search.Candidates)
	}
}

func TestUndoingALinkMadeAtSubmissionGivesTheRecordItsOwnTransaction(t *testing.T) {
	f := newFixture(t)
	report := bakeryReport("message-1")
	report.PaymentMethod, report.DateReference = nil, nil
	told := f.record(report).Transaction
	observation := f.walletObservation(walletPurchase("0|wallet|7|null|10123", "R$ 32,97 com Visa •••• 1234"))
	reading := purchaseReading(observation, brl(3297))
	reading.RelatedTransactionID = &told.ID
	linked := f.interpret(reading).Transaction
	conversation := confirmedSources(f.detail(told.ID))[ledger.SourceConversation]

	_, creatorErr := f.ledger.UndoMatch(t.Context(), f.account, ledger.UndoRequest{TransactionID: told.ID, ObservationID: conversation, Reason: "Errado"}, agent)
	undone := f.undoMatch(told.ID, observation)

	assertValidation(t, creatorErr, "observation_id")
	if undone.Effect != ledger.UndoSeparated || len(undone.Transactions) != 2 {
		t.Fatalf("undo = %+v, want the notification on its own transaction", undone)
	}
	restored := f.detail(told.ID)
	if linked.PaymentMethod == nil || linked.Occurred.Precision != calendar.PrecisionExact {
		t.Fatalf("linked = %+v, want the card and the exact time filled by the notification", linked)
	}
	if restored.PaymentMethod != nil || restored.Occurred.Precision != calendar.PrecisionUnknown {
		t.Errorf("restored = %+v, want the card and the exact time the notification filled removed", restored.Transaction)
	}
	separated := f.detail(undone.Transactions[1].ID)
	if separated.Amount != brl(3297) || separated.PaymentMethod == nil || *separated.PaymentMethod.CardLast4 != "1234" || separated.Category.Code != "meals" {
		t.Errorf("separated = %+v %+v, want the notification's purchase", separated.Transaction, separated.Category)
	}
	if sources := confirmedSources(separated); sources[ledger.SourceWalletNotification] != observation {
		t.Errorf("separated evidence = %v", sources)
	}
	if search := f.findMatches(ledger.MatchRef{TransactionID: &told.ID}); len(search.Candidates) != 0 {
		t.Errorf("candidates = %+v, want the separated pair never offered again", search.Candidates)
	}
}

func TestARejectedPairIsNeverOfferedAgain(t *testing.T) {
	f := newFixture(t)
	charged := f.submitStatement(bakeryStatement()).Lines[0].Transaction
	report := bakeryReport("message-1")
	report.DateReference, report.OccurredOn = nil, day("2026-10-19")
	told := f.record(report).Transaction
	before := f.findMatches(ledger.MatchRef{TransactionID: &told.ID})

	rejected, err := f.ledger.RejectMatch(t.Context(), f.account, ledger.MatchDecision{TransactionID: told.ID, OtherTransactionID: charged.ID, Reason: "Rudolfo disse que foram duas idas à padaria"}, agent)
	if err != nil {
		t.Fatal(err)
	}
	after := f.findMatches(ledger.MatchRef{TransactionID: &told.ID})

	if len(before.Candidates) != 1 || len(after.Candidates) != 0 || len(rejected.Transactions) != 2 {
		t.Errorf("candidates before = %d, after = %d, want the rejected pair gone", len(before.Candidates), len(after.Candidates))
	}
}

func TestADeferredMatchWaitsForRudolfoAndIsSettledByTheDecision(t *testing.T) {
	f := newFixture(t)
	notified := f.notifiedBakery("0|wallet|7|null|10123")
	report := bakeryReport("message-1")
	report.ReceivedAt = postedAt.Add(time.Hour)
	report.DeferMatch = true

	deferred := f.record(report)
	clarifications, err := f.ledger.Clarifications(t.Context(), f.account, ledger.ClarificationFilter{Kinds: []ledger.ClarificationKind{ledger.ClarificationPossibleMatch}})
	if err != nil {
		t.Fatal(err)
	}
	merged := f.confirmMatch(deferred.Transaction.ID, notified.ID)
	after, err := f.ledger.Clarifications(t.Context(), f.account, ledger.ClarificationFilter{Kinds: []ledger.ClarificationKind{ledger.ClarificationPossibleMatch}})
	if err != nil {
		t.Fatal(err)
	}

	if deferred.Result != ledger.ExpenseRecorded || deferred.Transaction.Status != ledger.StatusPendingClarification {
		t.Fatalf("deferred = %s %s, want recorded pending clarification", deferred.Result, deferred.Transaction.Status)
	}
	if len(clarifications) != 1 || *clarifications[0].TransactionID != deferred.Transaction.ID {
		t.Fatalf("clarifications = %+v, want one possible_match on the new record", clarifications)
	}
	if merged.Survivor.ID != notified.ID || merged.Survivor.Status != ledger.StatusProvisional || len(after) != 0 {
		t.Errorf("survivor = %+v, open possible matches = %d, want the question closed by the merge", merged.Survivor, len(after))
	}
}

func TestDismissingADeferredMatchKeepsTwoPurchases(t *testing.T) {
	f := newFixture(t)
	notified := f.notifiedBakery("0|wallet|7|null|10123")
	report := bakeryReport("message-1")
	report.ReceivedAt = postedAt.Add(time.Hour)
	report.PaymentMethod = &ledger.PaymentMethodRef{Kind: ledger.PaymentDebitCard}
	report.DeferMatch = true
	deferred := f.record(report).Transaction
	open, err := f.ledger.Clarifications(t.Context(), f.account, ledger.ClarificationFilter{Kinds: []ledger.ClarificationKind{ledger.ClarificationPossibleMatch}})
	if err != nil {
		t.Fatal(err)
	}

	_, resolveErr := f.ledger.ResolveClarification(t.Context(), f.account, open[0].ID, ledger.Resolution{Action: ledger.ResolutionResolve, Note: "É a mesma"}, ledger.Actor{Author: ledger.AuthorUser})
	_, err = f.ledger.ResolveClarification(t.Context(), f.account, open[0].ID, ledger.Resolution{Action: ledger.ResolutionDismiss, Note: "São duas compras"}, ledger.Actor{Author: ledger.AuthorUser})
	if err != nil {
		t.Fatal(err)
	}

	assertValidation(t, resolveErr, "action")
	if got := f.detail(deferred.ID).Status; got != ledger.StatusConfirmed {
		t.Errorf("status = %s, want confirmed like any debit purchase once the question is settled", got)
	}
	if search := f.findMatches(ledger.MatchRef{TransactionID: &notified.ID}); len(search.Candidates) != 0 {
		t.Errorf("candidates = %+v, want the dismissed pair never offered again", search.Candidates)
	}
}

func TestRefundsFollowTheMergedPurchaseAndComeBackOnUndo(t *testing.T) {
	f := newFixture(t)
	told := f.roundedReport()
	refundObservation := f.walletObservation(walletPurchase("0|wallet|9|null|10123", "Estorno de R$ 10,00"))
	refund := f.interpret(ledger.NotificationInterpretation{
		ObservationID: refundObservation, EventType: ledger.EventRefund, Amount: ptr(brl(1000)), RelatedTransactionID: &told.ID,
	}).Transaction
	notified := f.notifiedBakery("0|wallet|7|null|10123")
	conversation := confirmedSources(f.detail(told.ID))[ledger.SourceConversation]

	f.confirmMatch(told.ID, notified.ID)
	afterMerge := f.detail(refund.ID).RefundOfID
	f.undoMatch(notified.ID, conversation)
	afterUndo := f.detail(refund.ID).RefundOfID

	if afterMerge == nil || *afterMerge != notified.ID {
		t.Errorf("refund of = %v after the merge, want the survivor %s", afterMerge, notified.ID)
	}
	if afterUndo == nil || *afterUndo != told.ID {
		t.Errorf("refund of = %v after the undo, want the original purchase %s", afterUndo, told.ID)
	}
}

func TestAStatementLineCanWaitForRudolfoWithoutBlockingTheStatement(t *testing.T) {
	f := newFixture(t)
	told := f.record(bakeryReport("message-1")).Transaction
	statement := bakeryStatement()
	statement.Items[0].DeferMatch = true

	out := f.submitStatement(statement)

	if out.Result != ledger.StatementRecorded || out.Lines[0].Effect != ledger.EffectCreated {
		t.Fatalf("submission = %s, line = %s, want the statement recorded with the line on its own", out.Result, out.Lines[0].Effect)
	}
	if got := out.Lines[0].Transaction.Status; got != ledger.StatusPendingClarification {
		t.Errorf("line status = %s, want pending_clarification while Rudolfo decides", got)
	}
	open, err := f.ledger.Clarifications(t.Context(), f.account, ledger.ClarificationFilter{Kinds: []ledger.ClarificationKind{ledger.ClarificationPossibleMatch}})
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 1 || open[0].Context["candidate_ids"].([]any)[0] != told.ID.String() {
		t.Errorf("possible matches = %+v, want one naming the conversation record", open)
	}
}
