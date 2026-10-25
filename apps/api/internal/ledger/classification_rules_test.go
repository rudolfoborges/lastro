package ledger_test

import (
	"testing"

	"github.com/rudolfoborges/lastro/apps/api/internal/ledger"
)

func (f *fixture) bakeryRule() ledger.ClassificationRule {
	f.t.Helper()
	rule, err := f.ledger.CreateClassificationRule(f.t.Context(), f.account, ledger.NewClassificationRule{
		Merchant: "Padaria Pão Quente", Category: "meals", Instruction: "Sempre que for na Padaria Pão Quente, é café da manhã",
	}, rudolfo)
	if err != nil {
		f.t.Fatalf("CreateClassificationRule() error = %v", err)
	}
	return rule
}

func TestAOneOffClassificationNeverBecomesARule(t *testing.T) {
	f := newFixture(t)
	told := f.record(bakeryReport("message-1")).Transaction

	if _, err := f.ledger.Categorize(t.Context(), f.account, told.ID, "groceries", ledger.AllocationConfirmed, "Rudolfo disse que foi compra de mercado", rudolfo); err != nil {
		t.Fatal(err)
	}
	_, missing := f.ledger.CreateClassificationRule(t.Context(), f.account, ledger.NewClassificationRule{Merchant: "Padaria", Category: "meals"}, rudolfo)

	if got := f.count("classification_rules"); got != 0 {
		t.Errorf("rules = %d, want none from a one-off classification", got)
	}
	assertValidation(t, missing, "instruction")
}

func TestRulesAreListedArchivedAndUniquePerStore(t *testing.T) {
	f := newFixture(t)
	rule := f.bakeryRule()
	_, duplicate := f.ledger.CreateClassificationRule(t.Context(), f.account, ledger.NewClassificationRule{
		Merchant: "PADARIA PAO QUENTE", Category: "groceries", Instruction: "Na padaria é sempre mercado",
	}, rudolfo)

	archived, err := f.ledger.ArchiveClassificationRule(t.Context(), f.account, rule.ID, "Rudolfo mudou de ideia", rudolfo)
	if err != nil {
		t.Fatal(err)
	}
	active, err := f.ledger.ClassificationRules(t.Context(), f.account, false)
	if err != nil {
		t.Fatal(err)
	}
	all, err := f.ledger.ClassificationRules(t.Context(), f.account, true)
	if err != nil {
		t.Fatal(err)
	}

	assertValidation(t, duplicate, "merchant")
	if archived.Status != ledger.RuleArchived || len(active) != 0 || len(all) != 1 {
		t.Errorf("archived = %+v, active = %d, all = %d", archived, len(active), len(all))
	}
}

func TestARuleCaseAppliesIsRecordedWithOriginRule(t *testing.T) {
	f := newFixture(t)
	rule := f.bakeryRule()
	report := bakeryReport("message-1")
	report.Category, report.CategoryRuleID = nil, &rule.ID

	recorded := f.record(report)
	conflicting := bakeryReport("message-2")
	conflicting.Category, conflicting.CategoryRuleID = ptr("groceries"), &rule.ID
	_, err := f.ledger.RecordExpense(t.Context(), f.account, conflicting, agent)

	if recorded.Category.Code != "meals" || recorded.Category.Origin != ledger.AuthorRule || recorded.Category.Status != ledger.AllocationSuggested {
		t.Errorf("category = %+v, want meals suggested by the rule", recorded.Category)
	}
	assertValidation(t, err, "category")
	if _, err := f.ledger.ArchiveClassificationRule(t.Context(), f.account, rule.ID, "Não vale mais", rudolfo); err != nil {
		t.Fatal(err)
	}
	_, archived := f.ledger.CategorizeByRule(t.Context(), f.account, recorded.Transaction.ID, rule.ID, agent)
	assertValidation(t, archived, "rule_id")
}

func TestAcceptanceCriterion13AConfirmedCategoryIsNeverOverwritten(t *testing.T) {
	f := newFixture(t)
	rule := f.bakeryRule()
	told := f.record(bakeryReport("message-1")).Transaction
	if _, err := f.ledger.Categorize(t.Context(), f.account, told.ID, "convenience_snacks", ledger.AllocationConfirmed, "Rudolfo disse que foram refrigerantes", rudolfo); err != nil {
		t.Fatal(err)
	}

	suggested, err := f.ledger.Categorize(t.Context(), f.account, told.ID, "meals", ledger.AllocationSuggested, "Classificado pela Case.", agent)
	if err != nil {
		t.Fatal(err)
	}
	byRule, err := f.ledger.CategorizeByRule(t.Context(), f.account, told.ID, rule.ID, agent)
	if err != nil {
		t.Fatal(err)
	}
	statement := bakeryStatement()
	statement.Items[0].RelatedTransactionID, statement.Items[0].Category = &told.ID, ptr("meals")
	f.submitStatement(statement)

	detail := f.detail(told.ID)
	if detail.Category.Code != "convenience_snacks" || detail.Category.Status != ledger.AllocationConfirmed {
		t.Errorf("category = %+v, want the one Rudolfo confirmed", detail.Category)
	}
	if suggested.Conflict == nil || byRule.Conflict == nil {
		t.Errorf("conflicts = %+v / %+v, want each new classification turned into a question", suggested.Conflict, byRule.Conflict)
	}
	conflicts := 0
	for _, c := range detail.Clarifications {
		if c.Kind == ledger.ClarificationFieldConflict {
			conflicts++
		}
	}
	if conflicts != 1 {
		t.Errorf("open conflicts = %d, want one question about the category", conflicts)
	}
}
