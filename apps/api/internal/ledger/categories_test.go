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

func (f *fixture) categorize(tr ledger.Transaction, code string, status ledger.AllocationStatus, actor ledger.Actor) (ledger.Categorization, error) {
	f.t.Helper()
	return f.ledger.Categorize(f.t.Context(), f.account, tr.ID, code, status, "classificação", actor)
}

func (f *fixture) asTenant(fn func(pgx.Tx) error) error {
	f.t.Helper()
	conn, err := pgx.Connect(f.t.Context(), f.databaseURL)
	if err != nil {
		f.t.Fatal(err)
	}
	defer func() { _ = conn.Close(context.Background()) }()
	return pgx.BeginFunc(f.t.Context(), conn, func(tx pgx.Tx) error {
		if _, err := tx.Exec(f.t.Context(), "SET LOCAL ROLE lastro_app"); err != nil {
			return err
		}
		if _, err := tx.Exec(f.t.Context(), "SELECT set_config('app.account_id', $1, true)", f.account.String()); err != nil {
			return err
		}
		return fn(tx)
	})
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

func TestACategoryCoversTheWholeTransaction(t *testing.T) {
	f := newFixture(t)
	created, _ := f.transaction("message-1")

	result, err := f.categorize(created, "groceries", ledger.AllocationSuggested, agent)

	if err != nil || !result.Changed {
		t.Fatalf("Categorize() = %+v, %v", result, err)
	}
	if result.Category.Code != "groceries" || result.Category.Label != "Mercado" || result.Category.Amount != brl(32739) || result.Category.Origin != ledger.AuthorAgent {
		t.Errorf("category = %+v", result.Category)
	}

	changed, err := f.categorize(created, "convenience_snacks", ledger.AllocationConfirmed, rudolfo)
	if err != nil || !changed.Changed || changed.Category.Code != "convenience_snacks" {
		t.Fatalf("recategorize = %+v, %v", changed, err)
	}
	current, err := f.ledger.CategoryOf(t.Context(), f.account, created.ID)
	if err != nil || current.Code != "convenience_snacks" || current.Status != ledger.AllocationConfirmed {
		t.Errorf("CategoryOf() = %+v, %v", current, err)
	}
}

func TestATransactionWithoutCategoryCountsAsUnidentified(t *testing.T) {
	f := newFixture(t)
	created, _ := f.transaction("message-1")

	category, err := f.ledger.CategoryOf(t.Context(), f.account, created.ID)

	if err != nil {
		t.Fatal(err)
	}
	if category.Code != "unidentified" || category.Label != "Não identificado" || category.Amount != brl(32739) || category.Status != ledger.AllocationMissing {
		t.Errorf("category = %+v", category)
	}
}

func TestACategoryConfirmedByRudolfoIsNotOverwrittenWhenThePurchaseIsCheckedAgain(t *testing.T) {
	f := newFixture(t)
	created, _ := f.transaction("message-1")
	if _, err := f.categorize(created, "convenience_snacks", ledger.AllocationConfirmed, rudolfo); err != nil {
		t.Fatal(err)
	}

	for _, author := range []ledger.Author{ledger.AuthorAgent, ledger.AuthorRule} {
		result, err := f.categorize(created, "fuel", ledger.AllocationSuggested, ledger.Actor{Author: author})
		if err != nil {
			t.Fatal(err)
		}
		if result.Changed || result.Conflict == nil || result.Category.Code != "convenience_snacks" {
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

	_, err := f.categorize(created, "groceries", ledger.AllocationConfirmed, agent)

	assertValidation(t, err, "category_status")
}

func TestUnknownAndArchivedCategoriesAreRejectedWithTheValidCodes(t *testing.T) {
	f := newFixture(t)
	created, _ := f.transaction("message-1")

	_, err := f.categorize(created, "pets", ledger.AllocationSuggested, agent)
	var validation *ledger.ValidationError
	if !errors.As(err, &validation) || !strings.Contains(validation.Message, "groceries") {
		t.Errorf("unknown category error = %v, want the valid codes", err)
	}

	if _, err := f.ledger.ArchiveCategory(t.Context(), f.account, "education"); err != nil {
		t.Fatal(err)
	}
	_, err = f.categorize(created, "education", ledger.AllocationSuggested, agent)
	assertValidation(t, err, "category")
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

func TestTheCategoryFollowsAnAmountCorrection(t *testing.T) {
	f := newFixture(t)
	created, _ := f.transaction("message-1")
	if _, err := f.categorize(created, "groceries", ledger.AllocationConfirmed, rudolfo); err != nil {
		t.Fatal(err)
	}

	correction, err := f.ledger.Correct(t.Context(), f.account, created.ID, ledger.Patch{Amount: ptr(brl(10000))}, "valor errado", rudolfo)

	if err != nil {
		t.Fatal(err)
	}
	if correction.Category.Code != "groceries" || correction.Category.Amount != brl(10000) || correction.Category.Status != ledger.AllocationConfirmed {
		t.Errorf("category = %+v", correction.Category)
	}
}

func TestTheDatabaseKeepsOneCategoryMatchingTheAmount(t *testing.T) {
	f := newFixture(t)
	created, _ := f.transaction("message-1")
	if _, err := f.categorize(created, "groceries", ledger.AllocationSuggested, agent); err != nil {
		t.Fatal(err)
	}

	amountErr := f.asTenant(func(tx pgx.Tx) error {
		_, err := tx.Exec(t.Context(), "UPDATE transactions SET amount_minor = 100 WHERE id = $1", created.ID)
		return err
	})
	secondErr := f.asTenant(func(tx pgx.Tx) error {
		_, err := tx.Exec(t.Context(), `
			INSERT INTO category_allocations (id, account_id, transaction_id, category_id, amount_minor, status, origin)
			SELECT uuidv7(), account_id, $1, id, 1, 'suggested', 'agent' FROM categories WHERE code = 'meals'`, created.ID)
		return err
	})

	var pgErr *pgconn.PgError
	if !errors.As(amountErr, &pgErr) || pgErr.ConstraintName != "category_allocations_match_amount" {
		t.Errorf("changing the amount behind the category error = %v, want the amount constraint", amountErr)
	}
	if !errors.As(secondErr, &pgErr) || pgErr.ConstraintName != "category_allocations_one_current_idx" {
		t.Errorf("a second current category error = %v, want the single-category index", secondErr)
	}
	if detail, _ := f.ledger.GetTransaction(t.Context(), f.account, created.ID); detail.Amount != brl(32739) || detail.Category.Code != "groceries" {
		t.Errorf("transaction = %+v, want unchanged", detail)
	}
}
