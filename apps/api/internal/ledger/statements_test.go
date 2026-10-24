package ledger_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rudolfoborges/lastro/apps/api/internal/calendar"
	"github.com/rudolfoborges/lastro/apps/api/internal/ledger"
	"github.com/rudolfoborges/lastro/apps/api/internal/money"
)

var statementReceivedAt = time.Date(2026, 10, 10, 15, 0, 0, 0, time.UTC)

func day(value string) *calendar.Date {
	d, err := calendar.ParseDate(value)
	if err != nil {
		panic(err)
	}
	return &d
}

func line(kind ledger.StatementItemKind, text string, minor int64, date string, page int) ledger.StatementItem {
	item := ledger.StatementItem{Kind: kind, OriginalDescription: text, Amount: brl(minor), Page: page, Excerpt: date + " " + text}
	if date != "" {
		item.Date = day(date)
	}
	return item
}

func syntheticStatement() ledger.StatementExtraction {
	purchase := line(ledger.ItemPurchase, "PADARIA EXEMPLO", 3297, "2026-09-14", 2)
	purchase.Category = ptr("meals")
	international := line(ledger.ItemPurchase, "EXAMPLE SOFTWARE INC", 11000, "2026-09-20", 3)
	international.OriginalAmount = &money.Money{Minor: 2000, Currency: "USD"}
	international.Category = ptr("software_subscriptions")
	iof := line(ledger.ItemTax, "IOF TRANSACAO EXTERIOR", 385, "2026-09-20", 3)
	iof.Category = ptr("financial_charges")
	installment := line(ledger.ItemInstallment, "LOJA DE MOVEIS 03/10", 10000, "2026-07-02", 2)
	installment.Installment = &ledger.InstallmentPosition{Number: 3, Count: 10}
	pix := line(ledger.ItemPixInstallment, "PIX PARCELADO 02/12", 50000, "2026-08-10", 4)
	pix.Installment = &ledger.InstallmentPosition{Number: 2, Count: 12}
	pix.Components = []ledger.ChargeComponent{
		{Kind: ledger.ComponentPrincipal, Amount: brl(45000), Included: true},
		{Kind: ledger.ComponentInterest, Amount: brl(5000), Included: true},
	}
	additional := line(ledger.ItemPurchase, "POSTO EXEMPLO", 20000, "2026-09-22", 2)
	additional.CardLast4 = ptr("5678")
	additional.Category = ptr("fuel")
	fee := line(ledger.ItemFee, "ANUIDADE 05/12", 2500, "", 4)
	fee.Installment = &ledger.InstallmentPosition{Number: 5, Count: 12}
	fee.Category = ptr("financial_charges")
	refund := line(ledger.ItemRefund, "ESTORNO LOJA EXEMPLO", 3000, "2026-09-25", 3)
	refund.ConfirmDistinct = true
	unknown := line(ledger.ItemUnknown, "AJ CRED 0001", 1234, "2026-09-28", 4)
	unknown.Direction = ledger.DirectionCredit
	return ledger.StatementExtraction{
		Issuer:          "Banco Exemplo",
		CardLast4:       "1234",
		ClosingDate:     *day("2026-10-03"),
		DueDate:         *day("2026-10-10"),
		IssueDate:       day("2026-10-03"),
		PeriodStart:     day("2026-09-04"),
		PeriodEnd:       day("2026-10-03"),
		Total:           brl(98386),
		PreviousBalance: ptr(brl(150000)),
		Reading:         ledger.ReadingComplete,
		PageCount:       ptr(4),
		Items: []ledger.StatementItem{
			line(ledger.ItemPayment, "PAGAMENTO EFETUADO", 150000, "2026-09-10", 1),
			purchase, international, iof, installment, pix, additional, fee, refund, unknown,
		},
		Doubts: []ledger.ReadingDoubt{{Page: 4, Excerpt: "Encargos rotativos: ver tabela", Question: "A tabela de encargos da página 4 é só informativa?"}},
	}
}

func (f *fixture) submitStatement(in ledger.StatementExtraction) ledger.StatementSubmission {
	f.t.Helper()
	out, err := f.ledger.SubmitStatement(f.t.Context(), f.account, in, statementReceivedAt, agent)
	if err != nil {
		f.t.Fatalf("SubmitStatement() error = %v", err)
	}
	return out
}

func TestACompleteStatementBecomesLinesWithTheirAnchors(t *testing.T) {
	f := newFixture(t)

	out := f.submitStatement(syntheticStatement())

	if out.Result != ledger.StatementRecorded || out.Statement == nil || out.Statement.Version.Number != 1 || len(out.Lines) != 10 {
		t.Fatalf("submission = %+v", out)
	}
	effects := map[ledger.StatementItemKind][]ledger.InterpretationEffect{}
	for i, l := range out.Lines {
		if l.Position != i+1 || l.Page == 0 || l.Excerpt == "" {
			t.Errorf("line %d = %+v, want its position and anchor", i, l)
		}
		effects[l.Kind] = append(effects[l.Kind], l.Effect)
	}
	if effects[ledger.ItemInstallment][0] != ledger.EffectNone || effects[ledger.ItemPixInstallment][0] != ledger.EffectNone || effects[ledger.ItemUnknown][0] != ledger.EffectNone {
		t.Errorf("effects = %v; installments and unknown lines never become transactions in M3", effects)
	}
	natures := map[ledger.Nature]int{}
	for _, l := range out.Lines {
		if l.Transaction == nil {
			continue
		}
		natures[l.Transaction.Nature]++
		if l.Transaction.Status != ledger.StatusConfirmed || l.Transaction.Occurred.Source != calendar.SourceDocument && l.Transaction.Occurred.Known() {
			t.Errorf("transaction = %+v, want confirmed with the statement date", l.Transaction)
		}
	}
	if natures[ledger.NatureExpense] != 3 || natures[ledger.NatureCharge] != 2 || natures[ledger.NaturePayment] != 1 || natures[ledger.NatureRefund] != 1 {
		t.Errorf("natures = %v", natures)
	}
	if card := out.Lines[6].Transaction.PaymentMethod.CardLast4; card == nil || *card != "5678" {
		t.Errorf("additional card line = %+v, want card 5678", out.Lines[6].Transaction.PaymentMethod)
	}
	if original := out.Lines[2].Transaction.OriginalAmount; original == nil || *original != (money.Money{Minor: 2000, Currency: "USD"}) {
		t.Errorf("international purchase original amount = %+v", original)
	}
	if out.Lines[2].Category.Code != "software_subscriptions" || out.Lines[2].Category.Status != ledger.AllocationSuggested {
		t.Errorf("category = %+v", out.Lines[2].Category)
	}
	doubts := 0
	for _, c := range out.Clarifications {
		if c.Kind == ledger.ClarificationReadingDoubt && c.StatementID != nil && *c.StatementID == out.Statement.ID {
			doubts++
		}
	}
	if doubts != 2 {
		t.Errorf("clarifications = %+v, want the reading doubt and the unknown line", out.Clarifications)
	}
}

func TestResendingTheSameStatementCreatesNothing(t *testing.T) {
	f := newFixture(t)
	first := f.submitStatement(syntheticStatement())
	transactions, observations := f.count("transactions"), f.count("observations")

	reordered := syntheticStatement()
	reordered.Items[0], reordered.Items[1] = reordered.Items[1], reordered.Items[0]
	again := f.submitStatement(reordered)

	if again.Result != ledger.StatementAlreadyRecorded || again.Statement.ID != first.Statement.ID || len(again.Lines) != len(first.Lines) {
		t.Fatalf("resubmission = %+v", again)
	}
	if f.count("transactions") != transactions || f.count("observations") != observations || f.count("statement_versions") != 1 {
		t.Error("resending the same statement wrote new records")
	}
	if again.Lines[1].Transaction == nil || again.Lines[1].Effect != ledger.EffectCreated {
		t.Errorf("echoed line = %+v, want the transaction it created", again.Lines[1])
	}
}

func TestTwoIdenticalLegitimateLinesAreTwoObservations(t *testing.T) {
	f := newFixture(t)
	in := syntheticStatement()
	twin := line(ledger.ItemPurchase, "PADARIA EXEMPLO", 3297, "2026-09-14", 2)
	twin.ConfirmDistinct = true
	in.Items = append(in.Items, twin)

	out := f.submitStatement(in)

	first, second := out.Lines[1], out.Lines[10]
	if first.ObservationID == second.ObservationID || first.Transaction.ID == second.Transaction.ID {
		t.Errorf("identical lines = %+v and %+v, want two observations and two transactions", first, second)
	}
}

func TestAPurchaseAlreadyRecordedIsOfferedBeforeAnythingIsWritten(t *testing.T) {
	f := newFixture(t)
	report := supermarket("message-1")
	report.RawText, report.Description = "Padaria 32,97 no crédito", "Padaria"
	report.Amount = ptr(brl(3297))
	report.DateReference, report.OccurredOn = nil, day("2026-09-14")
	report.PaymentMethod = &ledger.PaymentMethodRef{Kind: ledger.PaymentCreditCard}
	report.Category, report.CategoryStatus = ptr("meals"), ledger.AllocationConfirmed
	recorded := f.record(report).Transaction

	offered := f.submitStatement(syntheticStatement())

	if offered.Result != ledger.StatementPossibleMatch || len(offered.Candidates) != 1 || offered.Candidates[0].Position != 2 || offered.Candidates[0].Candidates[0].Transaction.ID != recorded.ID {
		t.Fatalf("submission = %+v, want the conversation record offered for line 2", offered)
	}
	if f.count("statements") != 0 || f.count("statement_lines") != 0 {
		t.Error("a possible match wrote the statement")
	}

	in := syntheticStatement()
	in.Items[1].RelatedTransactionID = &recorded.ID
	in.Items[1].Category = ptr("groceries")
	linked := f.submitStatement(in)

	got := linked.Lines[1]
	if got.Effect != ledger.EffectLinked || got.Transaction.ID != recorded.ID || got.Transaction.Status != ledger.StatusConfirmed {
		t.Errorf("linked line = %+v, want the conversation record confirmed by the statement", got)
	}
	if got.Category.Code != "meals" || got.Category.Status != ledger.AllocationConfirmed {
		t.Errorf("category = %+v, want the one Rudolfo confirmed", got.Category)
	}
	if card := got.Transaction.PaymentMethod.CardLast4; card == nil || *card != "1234" {
		t.Errorf("payment method = %+v, want the card filled from the statement", got.Transaction.PaymentMethod)
	}
}

func TestCardNumbersInTheReadingAreRedactedBeforeTheyAreStored(t *testing.T) {
	f := newFixture(t)
	in := syntheticStatement()
	in.Items[1].Excerpt = "14/09 PADARIA EXEMPLO 4111 1111 1111 1111 32,97"
	in.Doubts[0].Excerpt = "cartão 4111 1111 1111 1111"

	out := f.submitStatement(in)

	if strings.Contains(out.Lines[1].Excerpt, "4111 1111") {
		t.Errorf("line excerpt = %q, want the card number masked", out.Lines[1].Excerpt)
	}
	for _, c := range out.Clarifications {
		if excerpt, _ := c.Context["excerpt"].(string); strings.Contains(excerpt, "4111 1111") {
			t.Errorf("clarification context = %+v, want the card number masked", c.Context)
		}
	}
}

func TestLinkingToAMissingTransactionPointsToTheItem(t *testing.T) {
	f := newFixture(t)
	in := syntheticStatement()
	in.Items[1].RelatedTransactionID = ptr(uuid.New())

	_, err := f.ledger.SubmitStatement(t.Context(), f.account, in, statementReceivedAt, agent)

	var validation *ledger.ValidationError
	if !errors.As(err, &validation) || validation.Field != "items[1].related_transaction_id" {
		t.Errorf("error = %v", err)
	}
}

func TestItemsWithoutAnchorsOrWithComponentsThatDoNotAddUpAreRejected(t *testing.T) {
	f := newFixture(t)
	cases := map[string]func(*ledger.StatementExtraction){
		"items[1].page":    func(in *ledger.StatementExtraction) { in.Items[1].Page = 0 },
		"items[1].excerpt": func(in *ledger.StatementExtraction) { in.Items[1].Excerpt = " " },
		"items[5].components": func(in *ledger.StatementExtraction) {
			in.Items[5].Components[1].Amount = brl(5500)
		},
		"items[4].installment_number": func(in *ledger.StatementExtraction) { in.Items[4].Installment.Number = 11 },
		"items[9].direction":          func(in *ledger.StatementExtraction) { in.Items[9].Direction = "" },
		"missing":                     func(in *ledger.StatementExtraction) { in.Reading = ledger.ReadingPartial },
	}
	for field, change := range cases {
		in := syntheticStatement()
		change(&in)
		_, err := f.ledger.SubmitStatement(t.Context(), f.account, in, statementReceivedAt, agent)
		var validation *ledger.ValidationError
		if !errors.As(err, &validation) || validation.Field != field {
			t.Errorf("%s: error = %v", field, err)
		}
	}
	if f.count("statements") != 0 || f.count("transactions") != 0 {
		t.Error("a rejected statement left records behind")
	}
}

func TestAnUnknownCategoryRejectsTheWholeStatement(t *testing.T) {
	f := newFixture(t)
	in := syntheticStatement()
	in.Items[6].Category = ptr("pets")

	_, err := f.ledger.SubmitStatement(t.Context(), f.account, in, statementReceivedAt, agent)

	var validation *ledger.ValidationError
	if !errors.As(err, &validation) || validation.Field != "items[6].category" {
		t.Fatalf("error = %v", err)
	}
	if f.count("statements") != 0 || f.count("transactions") != 0 {
		t.Error("the statement was recorded halfway")
	}
}

func TestADifferentReadingOfTheSameStatementIsRefusedUntilVersionsExist(t *testing.T) {
	f := newFixture(t)
	f.submitStatement(syntheticStatement())
	changed := syntheticStatement()
	changed.Issuer = "BANCO  EXEMPLO"
	changed.Total = brl(98387)

	_, err := f.ledger.SubmitStatement(t.Context(), f.account, changed, statementReceivedAt, agent)

	if !errors.Is(err, ledger.ErrStatementVersionUnsupported) {
		t.Errorf("error = %v, want the same statement recognized by issuer, card and closing date", err)
	}
}
