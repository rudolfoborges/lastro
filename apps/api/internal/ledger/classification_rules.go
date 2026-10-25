package ledger

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/rudolfoborges/lastro/apps/api/internal/money"
	"github.com/rudolfoborges/lastro/apps/api/internal/store"
	"github.com/rudolfoborges/lastro/apps/api/internal/store/tenantdb"
)

type RuleStatus string

const (
	RuleSuggested RuleStatus = "suggested"
	RuleActive    RuleStatus = "active"
	RuleArchived  RuleStatus = "archived"
)

const (
	minRuleEvidence = 2
	maxRuleEvidence = 50
)

// RuleConditions narrow a rule beyond the store. Amounts are inclusive.
type RuleConditions struct {
	MinAmount     *money.Money
	MaxAmount     *money.Money
	CardLast4     *string
	PaymentMethod *PaymentKind
}

type ClassificationRule struct {
	ID             uuid.UUID
	Merchant       string
	CategoryCode   string
	CategoryLabel  string
	Purpose        *string
	Conditions     RuleConditions
	Instruction    string
	Status         RuleStatus
	Rationale      *string
	BasedOn        []uuid.UUID
	ArchivedReason *string
	CreatedAt      time.Time
	ApprovedAt     *time.Time
	ArchivedAt     *time.Time
}

type NewClassificationRule struct {
	Merchant    string
	Category    string
	Purpose     *string
	Conditions  RuleConditions
	Instruction string
}

// RuleSuggestion is a rule Case proposes from purchases Rudolfo confirmed; it
// never classifies anything until he approves it.
type RuleSuggestion struct {
	Merchant   string
	Category   string
	Purpose    *string
	Conditions RuleConditions
	Rationale  string
	BasedOn    []uuid.UUID
}

type ruleFacts struct {
	Amount  *money.Money
	Payment *PaymentMethodRef
}

func (s *Service) CreateClassificationRule(ctx context.Context, accountID uuid.UUID, in NewClassificationRule, actor Actor) (ClassificationRule, error) {
	var rule ClassificationRule
	err := s.inTx(ctx, accountID, func(t *tx) error {
		id, err := once(t, "create_classification_rule", actor, in, func() (uuid.UUID, error) {
			return t.createRule(in)
		})
		if err != nil {
			return err
		}
		rule, err = t.rule(id)
		return err
	})
	return rule, err
}

func (s *Service) SuggestClassificationRule(ctx context.Context, accountID uuid.UUID, in RuleSuggestion, actor Actor) (ClassificationRule, error) {
	var rule ClassificationRule
	err := s.inTx(ctx, accountID, func(t *tx) error {
		id, err := once(t, "suggest_classification_rule", actor, in, func() (uuid.UUID, error) {
			return t.suggestRule(in)
		})
		if err != nil {
			return err
		}
		rule, err = t.rule(id)
		return err
	})
	return rule, err
}

func (s *Service) ApproveClassificationRule(ctx context.Context, accountID, ruleID uuid.UUID, instruction string, actor Actor) (ClassificationRule, error) {
	var rule ClassificationRule
	err := s.inTx(ctx, accountID, func(t *tx) error {
		_, err := once(t, "approve_classification_rule", actor, []any{ruleID, instruction}, func() (uuid.UUID, error) {
			return ruleID, t.approveRule(ruleID, instruction)
		})
		if err != nil {
			return err
		}
		rule, err = t.rule(ruleID)
		return err
	})
	return rule, err
}

func (s *Service) ClassificationRules(ctx context.Context, accountID uuid.UUID, includeArchived bool) ([]ClassificationRule, error) {
	var rules []ClassificationRule
	err := s.inTx(ctx, accountID, func(t *tx) error {
		rows, err := t.q.ListClassificationRules(t.ctx, tenantdb.ListClassificationRulesParams{AccountID: t.accountID, IncludeArchived: includeArchived})
		if err != nil {
			return fmt.Errorf("list classification rules: %w", err)
		}
		rules = make([]ClassificationRule, len(rows))
		for i, row := range rows {
			rules[i] = toRule(tenantdb.GetClassificationRuleRow(row))
		}
		return nil
	})
	return rules, err
}

func (s *Service) ArchiveClassificationRule(ctx context.Context, accountID, ruleID uuid.UUID, reason string, actor Actor) (ClassificationRule, error) {
	var rule ClassificationRule
	err := s.inTx(ctx, accountID, func(t *tx) error {
		_, err := once(t, "archive_classification_rule", actor, []any{ruleID, reason}, func() (uuid.UUID, error) {
			reason, err := requiredText("reason", reason, maxReason)
			if err != nil {
				return uuid.UUID{}, err
			}
			if _, err := t.rule(ruleID); err != nil {
				return uuid.UUID{}, err
			}
			if err := t.q.ArchiveClassificationRule(t.ctx, tenantdb.ArchiveClassificationRuleParams{AccountID: t.accountID, ID: ruleID, ArchivedReason: &reason}); err != nil {
				return uuid.UUID{}, fmt.Errorf("archive classification rule: %w", err)
			}
			return ruleID, nil
		})
		if err != nil {
			return err
		}
		rule, err = t.rule(ruleID)
		return err
	})
	return rule, err
}

type ruleFields struct {
	merchant   string
	key        string
	category   Category
	purpose    *string
	conditions RuleConditions
}

func (t *tx) ruleFieldsOf(merchant, category string, purpose *string, conditions RuleConditions) (ruleFields, error) {
	var fields ruleFields
	var err error
	if fields.merchant, err = requiredText("merchant", merchant, maxShortText); err != nil {
		return fields, err
	}
	if fields.purpose, err = optionalText("purpose", purpose, maxShortText); err != nil {
		return fields, err
	}
	if fields.key = normalizedName(fields.merchant); fields.key == "" {
		return fields, &ValidationError{Field: "merchant", Message: "must contain letters or digits"}
	}
	if fields.category, err = t.category(category); err != nil {
		return fields, err
	}
	if fields.category.Archived {
		return fields, &ValidationError{Field: "category", Message: fmt.Sprintf("category %q is archived", fields.category.Code)}
	}
	fields.conditions = conditions
	return fields, validateConditions(conditions)
}

func validateConditions(c RuleConditions) error {
	for _, bound := range []struct {
		field  string
		amount *money.Money
	}{{"min_amount", c.MinAmount}, {"max_amount", c.MaxAmount}} {
		if bound.amount != nil && bound.amount.IsNegative() {
			return &ValidationError{Field: bound.field, Message: "must not be negative"}
		}
	}
	if c.MinAmount != nil && c.MaxAmount != nil {
		if c.MinAmount.Currency != c.MaxAmount.Currency {
			return &ValidationError{Field: "max_amount", Message: "must be in the same currency as min_amount"}
		}
		if c.MinAmount.Minor >= c.MaxAmount.Minor {
			return &ValidationError{Field: "max_amount", Message: "must be greater than min_amount"}
		}
	}
	if c.MaxAmount != nil && c.MaxAmount.IsZero() {
		return &ValidationError{Field: "max_amount", Message: "must be greater than zero"}
	}
	if c.PaymentMethod == nil && c.CardLast4 == nil {
		return nil
	}
	ref := PaymentMethodRef{Kind: PaymentCreditCard, CardLast4: c.CardLast4}
	if c.PaymentMethod != nil {
		ref.Kind = *c.PaymentMethod
	}
	return validatePaymentMethod(ref)
}

func (c RuleConditions) key() string {
	amount := func(m *money.Money) string {
		if m == nil {
			return ""
		}
		return m.String()
	}
	card, method := "", ""
	if c.CardLast4 != nil {
		card = *c.CardLast4
	}
	if c.PaymentMethod != nil {
		method = string(*c.PaymentMethod)
	}
	if amount(c.MinAmount)+amount(c.MaxAmount)+card+method == "" {
		return ""
	}
	return fmt.Sprintf("min=%s|max=%s|card=%s|method=%s", amount(c.MinAmount), amount(c.MaxAmount), card, method)
}

// unmet says which condition the facts of a purchase break, so a rule is
// never recorded as the origin of a category it does not cover.
func (c RuleConditions) unmet(f ruleFacts) string {
	for _, bound := range []struct {
		limit *money.Money
		below bool
	}{{c.MinAmount, true}, {c.MaxAmount, false}} {
		if bound.limit == nil {
			continue
		}
		switch {
		case f.Amount == nil:
			return "the rule depends on the amount and the purchase has none"
		case f.Amount.Currency != bound.limit.Currency:
			return fmt.Sprintf("the rule is for amounts in %s and the purchase is in %s", bound.limit.Currency, f.Amount.Currency)
		case bound.below && f.Amount.Minor < bound.limit.Minor:
			return fmt.Sprintf("the purchase amount %s is below the rule's minimum %s", f.Amount, bound.limit)
		case !bound.below && f.Amount.Minor > bound.limit.Minor:
			return fmt.Sprintf("the purchase amount %s is above the rule's maximum %s", f.Amount, bound.limit)
		}
	}
	if c.PaymentMethod != nil && (f.Payment == nil || f.Payment.Kind != *c.PaymentMethod) {
		return fmt.Sprintf("the rule is for purchases paid with %s", *c.PaymentMethod)
	}
	if c.CardLast4 != nil && (f.Payment == nil || f.Payment.CardLast4 == nil || *f.Payment.CardLast4 != *c.CardLast4) {
		return fmt.Sprintf("the rule is for the card ending in %s", *c.CardLast4)
	}
	return ""
}

func (t *tx) insertRule(fields ruleFields, status RuleStatus, instruction, rationale *string, basedOn []uuid.UUID) (uuid.UUID, error) {
	params := tenantdb.InsertClassificationRuleParams{
		ID:            store.NewID(),
		AccountID:     t.accountID,
		Merchant:      fields.merchant,
		MerchantKey:   fields.key,
		CategoryID:    fields.category.ID,
		Purpose:       fields.purpose,
		Instruction:   instruction,
		Status:        string(status),
		CardLast4:     fields.conditions.CardLast4,
		ConditionsKey: fields.conditions.key(),
		Rationale:     rationale,
		BasedOn:       basedOn,
	}
	if instruction != nil {
		approved := time.Now().UTC()
		params.ApprovedAt = &approved
	}
	for _, bound := range []struct {
		amount *money.Money
		minor  **int64
	}{{fields.conditions.MinAmount, &params.MinAmountMinor}, {fields.conditions.MaxAmount, &params.MaxAmountMinor}} {
		if bound.amount != nil {
			minor, currency := bound.amount.Minor, bound.amount.Currency
			*bound.minor, params.Currency = &minor, &currency
		}
	}
	if fields.conditions.PaymentMethod != nil {
		method := string(*fields.conditions.PaymentMethod)
		params.PaymentMethod = &method
	}
	row, err := t.q.InsertClassificationRule(t.ctx, params)
	switch {
	case isUniqueViolation(err, "classification_rules_active_idx"):
		return uuid.UUID{}, &ValidationError{Field: "merchant", Message: "already has an active rule with these conditions; archive it before creating another"}
	case isUniqueViolation(err, "classification_rules_suggested_idx"):
		return uuid.UUID{}, &ValidationError{Field: "merchant", Message: "already has a suggestion with these conditions waiting for Rudolfo"}
	case err != nil:
		return uuid.UUID{}, fmt.Errorf("insert classification rule: %w", err)
	}
	return row.ID, nil
}

func (t *tx) createRule(in NewClassificationRule) (uuid.UUID, error) {
	instruction, err := requiredText("instruction", in.Instruction, maxReason)
	if err != nil {
		return uuid.UUID{}, &ValidationError{Field: "instruction", Message: "is required: what Rudolfo said, in his words, asking for the rule to always hold"}
	}
	fields, err := t.ruleFieldsOf(in.Merchant, in.Category, in.Purpose, in.Conditions)
	if err != nil {
		return uuid.UUID{}, err
	}
	return t.insertRule(fields, RuleActive, &instruction, nil, nil)
}

func (t *tx) suggestRule(in RuleSuggestion) (uuid.UUID, error) {
	rationale, err := requiredText("rationale", in.Rationale, maxReason)
	if err != nil {
		return uuid.UUID{}, err
	}
	fields, err := t.ruleFieldsOf(in.Merchant, in.Category, in.Purpose, in.Conditions)
	if err != nil {
		return uuid.UUID{}, err
	}
	basedOn := slices.Clone(in.BasedOn)
	slices.SortFunc(basedOn, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
	basedOn = slices.Compact(basedOn)
	if len(basedOn) < minRuleEvidence || len(basedOn) > maxRuleEvidence {
		return uuid.UUID{}, &ValidationError{Field: "based_on", Message: fmt.Sprintf("must list between %d and %d different purchases Rudolfo confirmed", minRuleEvidence, maxRuleEvidence)}
	}
	for _, id := range basedOn {
		if err := t.confirmsRule(id, fields); err != nil {
			return uuid.UUID{}, err
		}
	}
	rules, err := t.q.ListClassificationRules(t.ctx, tenantdb.ListClassificationRulesParams{AccountID: t.accountID})
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("list classification rules: %w", err)
	}
	for _, rule := range rules {
		if rule.Status == string(RuleActive) && rule.MerchantKey == fields.key && rule.ConditionsKey == fields.conditions.key() {
			return uuid.UUID{}, &ValidationError{Field: "merchant", Message: "already has an active rule with these conditions"}
		}
	}
	return t.insertRule(fields, RuleSuggested, nil, &rationale, basedOn)
}

// confirmsRule accepts a purchase as the basis of a suggestion only when it
// is at the rule's store, meets its conditions and has the rule's category
// confirmed by Rudolfo himself.
func (t *tx) confirmsRule(id uuid.UUID, fields ruleFields) error {
	transaction, err := t.transaction(id)
	if errors.Is(err, ErrNotFound) {
		return &ValidationError{Field: "based_on", Message: fmt.Sprintf("%s is not a transaction of Rudolfo's", id)}
	}
	if err != nil {
		return err
	}
	if transaction.Status == StatusVoided || transaction.Status == StatusMerged {
		return &ValidationError{Field: "based_on", Message: fmt.Sprintf("%s is %s", id, transaction.Status)}
	}
	if transaction.Merchant == nil || normalizedName(*transaction.Merchant) != fields.key {
		return &ValidationError{Field: "based_on", Message: fmt.Sprintf("%s is not a purchase at %s", id, fields.merchant)}
	}
	allocation, err := t.currentCategory(id)
	if err != nil {
		return err
	}
	if allocation == nil || allocation.Status != AllocationConfirmed || allocation.Code != fields.category.Code {
		return &ValidationError{Field: "based_on", Message: fmt.Sprintf("%s does not have %s confirmed by Rudolfo", id, fields.category.Code)}
	}
	if unmet := fields.conditions.unmet(factsOf(transaction)); unmet != "" {
		return &ValidationError{Field: "based_on", Message: fmt.Sprintf("%s does not meet the conditions: %s", id, unmet)}
	}
	return nil
}

func (t *tx) approveRule(ruleID uuid.UUID, instruction string) error {
	instruction, err := requiredText("instruction", instruction, maxReason)
	if err != nil {
		return &ValidationError{Field: "instruction", Message: "is required: Rudolfo's words approving the rule"}
	}
	rule, err := t.rule(ruleID)
	if err != nil {
		return err
	}
	if rule.Status != RuleSuggested {
		return &ValidationError{Field: "rule_id", Message: fmt.Sprintf("is %s, not a suggestion waiting for approval", rule.Status)}
	}
	approved := time.Now().UTC()
	err = t.q.ApproveClassificationRule(t.ctx, tenantdb.ApproveClassificationRuleParams{AccountID: t.accountID, ID: ruleID, Instruction: &instruction, ApprovedAt: &approved})
	if isUniqueViolation(err, "classification_rules_active_idx") {
		return &ValidationError{Field: "rule_id", Message: "an active rule already covers this store with these conditions; archive it first"}
	}
	if err != nil {
		return fmt.Errorf("approve classification rule: %w", err)
	}
	return nil
}

func factsOf(transaction Transaction) ruleFacts {
	facts := ruleFacts{Amount: &transaction.Amount}
	if transaction.PaymentMethod != nil {
		facts.Payment = &PaymentMethodRef{Kind: transaction.PaymentMethod.Kind, CardLast4: transaction.PaymentMethod.CardLast4}
	}
	return facts
}

func (t *tx) rule(id uuid.UUID) (ClassificationRule, error) {
	row, err := t.q.GetClassificationRule(t.ctx, tenantdb.GetClassificationRuleParams{AccountID: t.accountID, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return ClassificationRule{}, fmt.Errorf("classification rule %s: %w", id, ErrNotFound)
	}
	if err != nil {
		return ClassificationRule{}, fmt.Errorf("classification rule: %w", err)
	}
	return toRule(row), nil
}

// applyRule makes the category of a classification the one of Rudolfo's rule,
// so that only what he asked for is ever recorded with origin rule.
func (t *tx) applyRule(ruleID *uuid.UUID, category **string, field string, facts ruleFacts) (*ClassificationRule, error) {
	if ruleID == nil {
		return nil, nil
	}
	rule, err := t.rule(*ruleID)
	if errors.Is(err, ErrNotFound) {
		return nil, &ValidationError{Field: field, Message: "is not a classification rule of Rudolfo's; list_classification_rules shows them"}
	}
	if err != nil {
		return nil, err
	}
	switch rule.Status {
	case RuleSuggested:
		return nil, &ValidationError{Field: field, Message: "is a suggestion Rudolfo has not approved; classify without it"}
	case RuleArchived:
		return nil, &ValidationError{Field: field, Message: "was archived; classify without it"}
	}
	if unmet := rule.Conditions.unmet(facts); unmet != "" {
		return nil, &ValidationError{Field: field, Message: unmet + "; classify without the rule"}
	}
	if *category != nil && **category != rule.CategoryCode {
		return nil, &ValidationError{Field: "category", Message: fmt.Sprintf("differs from %s, the category of the rule; send the rule's category or leave category out", rule.CategoryCode)}
	}
	code := rule.CategoryCode
	*category = &code
	return &rule, nil
}

func classifierOf(actor Actor, rule *ClassificationRule) (Actor, string) {
	if rule == nil {
		return actor, reasonClassified
	}
	actor.Author, actor.Ref = AuthorRule, "rule:"+rule.ID.String()
	return actor, truncateRunes("Regra de Rudolfo: "+rule.Instruction, maxReason)
}

func toRule(row tenantdb.GetClassificationRuleRow) ClassificationRule {
	rule := ClassificationRule{
		ID:             row.ID,
		Merchant:       row.Merchant,
		CategoryCode:   row.CategoryCode,
		CategoryLabel:  row.CategoryLabel,
		Purpose:        row.Purpose,
		Conditions:     RuleConditions{CardLast4: row.CardLast4},
		Status:         RuleStatus(row.Status),
		Rationale:      row.Rationale,
		BasedOn:        row.BasedOn,
		ArchivedReason: row.ArchivedReason,
		CreatedAt:      row.CreatedAt,
		ApprovedAt:     row.ApprovedAt,
		ArchivedAt:     row.ArchivedAt,
	}
	if row.Instruction != nil {
		rule.Instruction = *row.Instruction
	}
	if row.PaymentMethod != nil {
		method := PaymentKind(*row.PaymentMethod)
		rule.Conditions.PaymentMethod = &method
	}
	if row.Currency != nil {
		for _, bound := range []struct {
			minor  *int64
			target **money.Money
		}{{row.MinAmountMinor, &rule.Conditions.MinAmount}, {row.MaxAmountMinor, &rule.Conditions.MaxAmount}} {
			if bound.minor != nil {
				*bound.target = &money.Money{Minor: *bound.minor, Currency: *row.Currency}
			}
		}
	}
	return rule
}
