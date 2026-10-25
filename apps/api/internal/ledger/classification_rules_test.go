package ledger_test

import (
	"testing"

	"github.com/google/uuid"

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

func (f *fixture) confirmedAtBakery(requestID string, amount int64) *ledger.Transaction {
	f.t.Helper()
	report := bakeryReport(requestID)
	report.RawText = "Padaria " + requestID
	report.Amount = ptr(brl(amount))
	report.PaymentMethod = &ledger.PaymentMethodRef{Kind: ledger.PaymentCreditCard, CardLast4: ptr("1234")}
	transaction := f.record(report).Transaction
	if _, err := f.ledger.Categorize(f.t.Context(), f.account, transaction.ID, "meals", ledger.AllocationConfirmed, "Rudolfo disse que foi café da manhã", rudolfo); err != nil {
		f.t.Fatal(err)
	}
	return transaction
}

func TestARuleWithConditionsOnlyCoversThePurchasesThatMeetThem(t *testing.T) {
	f := newFixture(t)
	card := ledger.PaymentCreditCard
	breakfast, err := f.ledger.CreateClassificationRule(t.Context(), f.account, ledger.NewClassificationRule{
		Merchant: "Padaria Pão Quente", Category: "meals", Instruction: "Na padaria, até 20 reais no cartão final 1234 é café da manhã",
		Conditions: ledger.RuleConditions{MaxAmount: ptr(brl(2000)), CardLast4: ptr("1234"), PaymentMethod: &card},
	}, rudolfo)
	if err != nil {
		t.Fatal(err)
	}
	_, other := f.ledger.CreateClassificationRule(t.Context(), f.account, ledger.NewClassificationRule{
		Merchant: "Padaria Pão Quente", Category: "groceries", Instruction: "Acima de 20 reais na padaria é compra de mercado",
		Conditions: ledger.RuleConditions{MinAmount: ptr(brl(2001))},
	}, rudolfo)
	_, inverted := f.ledger.CreateClassificationRule(t.Context(), f.account, ledger.NewClassificationRule{
		Merchant: "Mercado", Category: "groceries", Instruction: "x", Conditions: ledger.RuleConditions{MinAmount: ptr(brl(5000)), MaxAmount: ptr(brl(1000))},
	}, rudolfo)

	attempt := func(requestID string, amount int64, last4 string) error {
		report := bakeryReport(requestID)
		report.RawText, report.Category, report.CategoryRuleID = "Padaria "+requestID, nil, &breakfast.ID
		report.Amount = ptr(brl(amount))
		report.PaymentMethod = &ledger.PaymentMethodRef{Kind: ledger.PaymentCreditCard, CardLast4: ptr(last4)}
		_, err := f.ledger.RecordExpense(t.Context(), f.account, report, agent)
		return err
	}

	if err := attempt("small", 1500, "1234"); err != nil {
		t.Errorf("a purchase that meets the rule = %v", err)
	}
	assertValidation(t, attempt("large", 3297, "1234"), "category_rule_id")
	assertValidation(t, attempt("other-card", 1500, "5678"), "category_rule_id")
	if other != nil {
		t.Errorf("a second rule for the same store with other conditions = %v", other)
	}
	assertValidation(t, inverted, "max_amount")
}

func TestARuleSuggestionNeedsConfirmedPurchasesAndRudolfosApproval(t *testing.T) {
	f := newFixture(t)
	first, second := f.confirmedAtBakery("message-1", 1500), f.confirmedAtBakery("message-2", 1250)
	suggested := bakeryReport("message-3")
	suggested.RawText, suggested.Amount = "Padaria message-3", ptr(brl(1100))
	unconfirmed := f.record(suggested).Transaction
	suggestion := func(basedOn ...uuid.UUID) ledger.RuleSuggestion {
		return ledger.RuleSuggestion{Merchant: "Padaria Pão Quente", Category: "meals", Rationale: "Rudolfo confirmou café da manhã nas duas últimas compras na padaria", BasedOn: basedOn}
	}

	_, single := f.ledger.SuggestClassificationRule(t.Context(), f.account, suggestion(first.ID), agent)
	_, notConfirmed := f.ledger.SuggestClassificationRule(t.Context(), f.account, suggestion(first.ID, unconfirmed.ID), agent)
	rule, err := f.ledger.SuggestClassificationRule(t.Context(), f.account, suggestion(first.ID, second.ID), agent)
	if err != nil {
		t.Fatal(err)
	}
	report := bakeryReport("message-4")
	report.RawText, report.Amount, report.Category, report.CategoryRuleID = "Padaria message-4", ptr(brl(900)), nil, &rule.ID
	_, beforeApproval := f.ledger.RecordExpense(t.Context(), f.account, report, agent)
	approved, err := f.ledger.ApproveClassificationRule(t.Context(), f.account, rule.ID, "Pode criar, é sempre café da manhã", rudolfo)
	if err != nil {
		t.Fatal(err)
	}
	recorded, afterApproval := f.ledger.RecordExpense(t.Context(), f.account, report, agent)
	_, again := f.ledger.SuggestClassificationRule(t.Context(), f.account, suggestion(first.ID, second.ID), agent)

	assertValidation(t, single, "based_on")
	assertValidation(t, notConfirmed, "based_on")
	if rule.Status != ledger.RuleSuggested || rule.Instruction != "" || len(rule.BasedOn) != 2 {
		t.Errorf("suggestion = %+v", rule)
	}
	assertValidation(t, beforeApproval, "category_rule_id")
	if approved.Status != ledger.RuleActive || approved.ApprovedAt == nil || approved.Instruction == "" {
		t.Errorf("approved = %+v", approved)
	}
	if afterApproval != nil || recorded.Category.Origin != ledger.AuthorRule {
		t.Errorf("after approval = %+v, %v", recorded.Category, afterApproval)
	}
	assertValidation(t, again, "merchant")
}

func TestARefusedSuggestionIsArchived(t *testing.T) {
	f := newFixture(t)
	first, second := f.confirmedAtBakery("message-1", 1500), f.confirmedAtBakery("message-2", 1250)
	rule, err := f.ledger.SuggestClassificationRule(t.Context(), f.account, ledger.RuleSuggestion{
		Merchant: "Padaria Pão Quente", Category: "meals", Rationale: "Duas compras confirmadas como refeição", BasedOn: []uuid.UUID{first.ID, second.ID},
	}, agent)
	if err != nil {
		t.Fatal(err)
	}

	refused, err := f.ledger.ArchiveClassificationRule(t.Context(), f.account, rule.ID, "Rudolfo disse que não quer essa regra", rudolfo)
	if err != nil {
		t.Fatal(err)
	}
	_, approve := f.ledger.ApproveClassificationRule(t.Context(), f.account, rule.ID, "sim", rudolfo)

	if refused.Status != ledger.RuleArchived {
		t.Errorf("refused = %+v", refused)
	}
	assertValidation(t, approve, "rule_id")
}
