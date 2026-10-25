package export_test

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rudolfoborges/lastro/apps/api/internal/accounts"
	"github.com/rudolfoborges/lastro/apps/api/internal/calendar"
	"github.com/rudolfoborges/lastro/apps/api/internal/export"
	"github.com/rudolfoborges/lastro/apps/api/internal/ledger"
	"github.com/rudolfoborges/lastro/apps/api/internal/money"
	"github.com/rudolfoborges/lastro/apps/api/internal/store"
	"github.com/rudolfoborges/lastro/apps/api/internal/store/storetest"
)

var now = time.Date(2026, 10, 24, 15, 0, 0, 0, time.UTC)

type database struct {
	store   *store.Store
	ledger  *ledger.Service
	exports *export.Service
}

func open(t *testing.T) database {
	t.Helper()
	s, err := store.Open(t.Context(), storetest.MigratedDatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return database{store: s, ledger: ledger.NewService(s), exports: export.NewService(s)}
}

func brl(minor int64) money.Money {
	return money.Money{Minor: minor, Currency: "BRL"}
}

func day(value string) *calendar.Date {
	d, err := calendar.ParseDate(value)
	if err != nil {
		panic(err)
	}
	return &d
}

func ptr[T any](v T) *T {
	return &v
}

func populate(t *testing.T, db database) uuid.UUID {
	t.Helper()
	svc := accounts.NewService(db.store)
	invite, err := svc.CreateInvite(t.Context(), time.Hour, "")
	if err != nil {
		t.Fatal(err)
	}
	account, _, err := svc.SignUp(t.Context(), invite.Code, "Rudolfo")
	if err != nil {
		t.Fatal(err)
	}
	agent := ledger.Actor{Author: ledger.AuthorAgent}
	purchase, err := db.ledger.RecordExpense(t.Context(), account.ID, ledger.ExpenseReport{
		RequestID: "message-1", RawText: "Luminária de 100 no débito", Description: "Luminária", Amount: ptr(brl(10000)),
		OccurredOn: day("2026-09-20"), PaymentMethod: &ledger.PaymentMethodRef{Kind: ledger.PaymentDebitCard}, Category: ptr("home"), ReceivedAt: now,
	}, agent)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.ledger.RecordExpense(t.Context(), account.ID, ledger.ExpenseReport{
		RequestID: "message-2", RawText: "Estorno de 30", Amount: ptr(brl(3000)), Nature: ledger.NatureRefund, RefundOfID: &purchase.Transaction.ID,
		OccurredOn: day("2026-10-02"), PaymentMethod: &ledger.PaymentMethodRef{Kind: ledger.PaymentDebitCard}, ReceivedAt: now,
	}, agent)
	if err != nil {
		t.Fatal(err)
	}
	installment := ledger.StatementItem{Kind: ledger.ItemPixInstallment, OriginalDescription: "PIX PARCELADO 02/06", Amount: brl(50000), Date: day("2026-08-10"), Page: 1,
		Excerpt: "PIX PARCELADO 02/06 500,00", Installment: &ledger.InstallmentPosition{Number: 2, Count: 6}, Category: ptr("home"),
		Components: []ledger.ChargeComponent{{Kind: ledger.ComponentPrincipal, Amount: brl(45000), Included: true}, {Kind: ledger.ComponentInterest, Amount: brl(5000), Included: true}}}
	_, err = db.ledger.SubmitStatement(t.Context(), account.ID, ledger.StatementExtraction{
		Issuer: "Banco Exemplo", CardLast4: "1234", ClosingDate: *day("2026-10-03"), DueDate: *day("2026-10-10"), PeriodStart: day("2026-09-04"),
		Total: brl(50000), Reading: ledger.ReadingComplete, Items: []ledger.StatementItem{installment},
	}, now, agent)
	if err != nil {
		t.Fatal(err)
	}
	return account.ID
}

type totals struct {
	purchases   []ledger.PurchaseTotals
	composition ledger.StatementComposition
	commitments []ledger.CommitmentTotals
}

func totalsOf(t *testing.T, db database, accountID uuid.UUID) totals {
	t.Helper()
	purchases, err := db.ledger.Purchases(t.Context(), accountID, *day("2026-08-01"), *day("2026-10-31"), ledger.ViewFilter{}, ledger.GroupNone)
	if err != nil {
		t.Fatal(err)
	}
	review, err := db.ledger.StatementReview(t.Context(), accountID, ledger.StatementRef{})
	if err != nil {
		t.Fatal(err)
	}
	commitments, err := db.ledger.FutureCommitments(t.Context(), accountID, ledger.ViewFilter{}, now)
	if err != nil {
		t.Fatal(err)
	}
	return totals{purchases: purchases.Totals, composition: review.Composition, commitments: commitments.Totals}
}

func TestAnExportRestoredInAnEmptyDatabaseReproducesTheTotals(t *testing.T) {
	source, target := open(t), open(t)
	accountID := populate(t, source)
	before := totalsOf(t, source, accountID)

	doc, err := source.exports.Dump(t.Context(), accountID, now)
	if err != nil {
		t.Fatal(err)
	}
	var file bytes.Buffer
	if err := source.exports.Write(t.Context(), &file, doc); err != nil {
		t.Fatal(err)
	}
	archive, err := zip.NewReader(bytes.NewReader(file.Bytes()), int64(file.Len()))
	if err != nil {
		t.Fatal(err)
	}
	inner, err := archive.Open(export.DocumentName)
	if err != nil {
		t.Fatal(err)
	}
	read, err := export.Read(inner)
	if err != nil {
		t.Fatal(err)
	}
	if err := target.exports.Restore(t.Context(), read); err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	after := totalsOf(t, target, accountID)

	if before.purchases[0].Confirmed.Amount != after.purchases[0].Confirmed.Amount || before.purchases[0].NetConfirmed() != after.purchases[0].NetConfirmed() {
		t.Errorf("purchases = %+v, restored %+v", before.purchases, after.purchases)
	}
	if before.composition.ComputedTotal != after.composition.ComputedTotal || before.composition.IncludedCharges != after.composition.IncludedCharges {
		t.Errorf("statement = %+v, restored %+v", before.composition, after.composition)
	}
	if before.commitments[0].Total() != after.commitments[0].Total() || after.commitments[0].Total() != brl(200000) {
		t.Errorf("commitments = %+v, restored %+v", before.commitments, after.commitments)
	}
	if err := target.exports.Restore(t.Context(), read); !errors.Is(err, export.ErrAccountExists) {
		t.Errorf("second restore error = %v, want ErrAccountExists", err)
	}
}

func TestTheCSVsCarryDecimalAmounts(t *testing.T) {
	db := open(t)
	accountID := populate(t, db)
	doc, err := db.exports.Dump(t.Context(), accountID, now)
	if err != nil {
		t.Fatal(err)
	}
	var file bytes.Buffer
	if err := db.exports.Write(t.Context(), &file, doc); err != nil {
		t.Fatal(err)
	}
	archive, err := zip.NewReader(bytes.NewReader(file.Bytes()), int64(file.Len()))
	if err != nil {
		t.Fatal(err)
	}
	inner, err := archive.Open("transactions.csv")
	if err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(inner).ReadAll()
	if err != nil {
		t.Fatal(err)
	}

	if len(rows) != 4 || rows[0][5] != "amount" {
		t.Fatalf("rows = %v, want a header and three transactions", rows)
	}
	amounts := map[string]bool{}
	for _, row := range rows[1:] {
		amounts[row[5]] = true
	}
	if !amounts["100.00"] || !amounts["30.00"] || !amounts["500.00"] {
		t.Errorf("amounts = %v, want decimals", amounts)
	}
}
