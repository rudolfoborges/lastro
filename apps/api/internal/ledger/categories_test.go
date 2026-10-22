package ledger_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/rudolfoborges/lastro/apps/api/internal/ledger"
	"github.com/rudolfoborges/lastro/apps/api/internal/taxonomy"
)

func split(parts ...any) []ledger.Split {
	var out []ledger.Split
	for i := 0; i < len(parts); i += 2 {
		out = append(out, ledger.Split{Code: parts[i].(string), Amount: ptr(brl(int64(parts[i+1].(int))))})
	}
	return out
}

func (f *fixture) categorize(id any, parts []ledger.Split, status ledger.AllocationStatus, actor ledger.Actor) (ledger.Categorization, error) {
	f.t.Helper()
	tr := id.(ledger.Transaction)
	return f.ledger.Categorize(f.t.Context(), f.account, tr.ID, parts, status, "classificação", actor)
}

func TestNewAccountsStartWithTheDefaultTaxonomy(t *testing.T) {
	f := newFixture(t)

	categories, err := f.ledger.Categories(t.Context(), f.account)

	if err != nil {
		t.Fatal(err)
	}
	if len(categories) != len(taxonomy.Defaults) {
		t.Fatalf("categories = %d, want %d", len(categories), len(taxonomy.Defaults))
	}
	byCode := map[string]string{}
	for _, c := range categories {
		byCode[c.Code] = c.Label
	}
	for _, c := range taxonomy.Defaults {
		if byCode[c.Code] != c.Label {
			t.Errorf("category %s = %q, want %q", c.Code, byCode[c.Code], c.Label)
		}
	}
}

func TestASingleCategoryTakesTheWholeAmount(t *testing.T) {
	f := newFixture(t)
	created, _ := f.transaction("message-1")

	result, err := f.categorize(created, []ledger.Split{{Code: "groceries"}}, ledger.AllocationSuggested, agent)

	if err != nil || !result.Changed {
		t.Fatalf("Categorize() = %+v, %v", result, err)
	}
	if len(result.Allocations) != 1 || result.Allocations[0].Amount != brl(32739) || result.Allocations[0].Label != "Mercado" || result.Allocations[0].Origin != ledger.AuthorAgent {
		t.Errorf("allocations = %+v", result.Allocations)
	}
}

func TestASplitMustMatchTheAmountToTheCent(t *testing.T) {
	f := newFixture(t)
	created, _ := f.transaction("message-1")

	_, err := f.categorize(created, split("groceries", 30000, "convenience_snacks", 2738), ledger.AllocationConfirmed, rudolfo)

	assertValidation(t, err, "categories")
	var validation *ledger.ValidationError
	if errors.As(err, &validation) && !strings.Contains(validation.Message, "R$ 327,38") {
		t.Errorf("message = %q, want the split total", validation.Message)
	}

	result, err := f.categorize(created, split("groceries", 30000, "convenience_snacks", 2739), ledger.AllocationConfirmed, rudolfo)
	if err != nil || len(result.Allocations) != 2 {
		t.Fatalf("exact split = %+v, %v", result, err)
	}
}

func TestATransactionWithoutCategoryCountsAsUnidentified(t *testing.T) {
	f := newFixture(t)
	created, _ := f.transaction("message-1")

	allocations, err := f.ledger.Allocations(t.Context(), f.account, created.ID)

	if err != nil {
		t.Fatal(err)
	}
	if len(allocations) != 1 || allocations[0].Code != "unidentified" || allocations[0].Label != "Não identificado" || allocations[0].Amount != brl(32739) || allocations[0].Status != ledger.AllocationMissing {
		t.Errorf("allocations = %+v", allocations)
	}
}

func TestACategoryConfirmedByRudolfoIsNotOverwrittenWhenThePurchaseIsCheckedAgain(t *testing.T) {
	f := newFixture(t)
	created, _ := f.transaction("message-1")
	if _, err := f.categorize(created, []ledger.Split{{Code: "convenience_snacks"}}, ledger.AllocationConfirmed, rudolfo); err != nil {
		t.Fatal(err)
	}

	for _, author := range []ledger.Author{ledger.AuthorAgent, ledger.AuthorRule} {
		result, err := f.categorize(created, []ledger.Split{{Code: "fuel"}}, ledger.AllocationSuggested, ledger.Actor{Author: author})
		if err != nil {
			t.Fatal(err)
		}
		if result.Changed || result.Conflict == nil || result.Allocations[0].Code != "convenience_snacks" {
			t.Errorf("%s recategorization = %+v, want the confirmed category kept and a conflict", author, result)
		}
	}
	history, err := f.ledger.History(t.Context(), f.account, created.ID)
	if err != nil || len(history) != 2 {
		t.Errorf("history = %+v, %v; want creation and Rudolfo's classification only", history, err)
	}
}

func TestOnlyRudolfoConfirmsACategory(t *testing.T) {
	f := newFixture(t)
	created, _ := f.transaction("message-1")

	_, err := f.categorize(created, []ledger.Split{{Code: "groceries"}}, ledger.AllocationConfirmed, agent)

	assertValidation(t, err, "category_status")
}

func TestUnknownAndArchivedCategoriesAreRejectedWithTheValidCodes(t *testing.T) {
	f := newFixture(t)
	created, _ := f.transaction("message-1")

	_, err := f.categorize(created, []ledger.Split{{Code: "pets"}}, ledger.AllocationSuggested, agent)
	var validation *ledger.ValidationError
	if !errors.As(err, &validation) || !strings.Contains(validation.Message, "groceries") {
		t.Errorf("unknown category error = %v, want the valid codes", err)
	}

	if _, err := f.ledger.ArchiveCategory(t.Context(), f.account, "education"); err != nil {
		t.Fatal(err)
	}
	_, err = f.categorize(created, []ledger.Split{{Code: "education"}}, ledger.AllocationSuggested, agent)
	assertValidation(t, err, "categories")
	_, err = f.ledger.ArchiveCategory(t.Context(), f.account, "unidentified")
	assertValidation(t, err, "code")
}

func TestCategoriesCanBeCreatedAndRenamed(t *testing.T) {
	f := newFixture(t)

	created, err := f.ledger.SaveCategory(t.Context(), f.account, "pet_care", "Pets")
	if err != nil || created.Label != "Pets" {
		t.Fatalf("SaveCategory(new) = %+v, %v", created, err)
	}
	renamed, err := f.ledger.SaveCategory(t.Context(), f.account, "pet_care", "Cuidados com pets")
	if err != nil || renamed.ID != created.ID || renamed.Label != "Cuidados com pets" {
		t.Errorf("SaveCategory(rename) = %+v, %v", renamed, err)
	}
	_, err = f.ledger.SaveCategory(t.Context(), f.account, "Pet Care", "Pets")
	assertValidation(t, err, "code")
}

func TestASingleCategoryFollowsAnAmountCorrection(t *testing.T) {
	f := newFixture(t)
	created, _ := f.transaction("message-1")
	if _, err := f.categorize(created, []ledger.Split{{Code: "groceries"}}, ledger.AllocationConfirmed, rudolfo); err != nil {
		t.Fatal(err)
	}

	correction, err := f.ledger.Correct(t.Context(), f.account, created.ID, ledger.Patch{Amount: ptr(brl(10000))}, "valor errado", rudolfo)

	if err != nil {
		t.Fatal(err)
	}
	if len(correction.Categories) != 1 || correction.Categories[0].Amount != brl(10000) || correction.Categories[0].Status != ledger.AllocationConfirmed {
		t.Errorf("categories = %+v", correction.Categories)
	}
}

func TestASplitNeedsANewSplitWhenTheAmountChanges(t *testing.T) {
	f := newFixture(t)
	created, _ := f.transaction("message-1")
	if _, err := f.categorize(created, split("groceries", 30000, "convenience_snacks", 2739), ledger.AllocationConfirmed, rudolfo); err != nil {
		t.Fatal(err)
	}

	_, err := f.ledger.Correct(t.Context(), f.account, created.ID, ledger.Patch{Amount: ptr(brl(10000))}, "valor errado", rudolfo)
	assertValidation(t, err, "categories")

	correction, err := f.ledger.Correct(t.Context(), f.account, created.ID, ledger.Patch{
		Amount:     ptr(brl(10000)),
		Categories: split("groceries", 8000, "convenience_snacks", 2000),
	}, "valor errado", rudolfo)
	if err != nil || len(correction.Categories) != 2 {
		t.Fatalf("correction with split = %+v, %v", correction, err)
	}
}

func TestTheDatabaseRefusesAllocationsThatDoNotMatchTheAmount(t *testing.T) {
	f := newFixture(t)
	created, _ := f.transaction("message-1")
	if _, err := f.categorize(created, []ledger.Split{{Code: "groceries"}}, ledger.AllocationSuggested, agent); err != nil {
		t.Fatal(err)
	}
	conn, err := pgx.Connect(t.Context(), f.databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(context.Background()) }()

	err = pgx.BeginFunc(t.Context(), conn, func(tx pgx.Tx) error {
		if _, err := tx.Exec(t.Context(), "SET LOCAL ROLE lastro_app"); err != nil {
			return err
		}
		if _, err := tx.Exec(t.Context(), "SELECT set_config('app.account_id', $1, true)", f.account.String()); err != nil {
			return err
		}
		_, err := tx.Exec(t.Context(), "UPDATE transactions SET amount_minor = 100 WHERE id = $1", created.ID)
		return err
	})

	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.ConstraintName != "category_allocations_match_amount" {
		t.Errorf("commit error = %v, want the allocation constraint", err)
	}
	if detail, _ := f.ledger.GetTransaction(t.Context(), f.account, created.ID); detail.Amount != brl(32739) {
		t.Errorf("amount = %s, want unchanged", detail.Amount)
	}
}
