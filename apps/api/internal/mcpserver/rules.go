package mcpserver

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/rudolfoborges/lastro/apps/api/internal/accounts"
	"github.com/rudolfoborges/lastro/apps/api/internal/ledger"
	"github.com/rudolfoborges/lastro/apps/api/internal/money"
)

type RuleConditionsOutput struct {
	MinAmount     *MoneyOutput `json:"min_amount,omitempty" jsonschema:"the purchase amount is at least this"`
	MaxAmount     *MoneyOutput `json:"max_amount,omitempty" jsonschema:"the purchase amount is at most this"`
	CardLast4     *string      `json:"card_last4,omitempty" jsonschema:"paid with the card ending in these digits"`
	PaymentMethod *string      `json:"payment_method,omitempty" jsonschema:"paid with this method"`
}

type ClassificationRuleOutput struct {
	ID             string                `json:"id" jsonschema:"send it as category_rule_id or rule_id when the rule decided a category"`
	Merchant       string                `json:"merchant" jsonschema:"store the rule is about, as Rudolfo named it"`
	Conditions     *RuleConditionsOutput `json:"conditions,omitempty" jsonschema:"what else a purchase must meet for the rule to apply; Lastro refuses the rule on a purchase that does not"`
	Category       CategoryOutput        `json:"category"`
	Purpose        *string               `json:"purpose,omitempty" jsonschema:"purpose Rudolfo attached to the rule, if any"`
	Instruction    string                `json:"instruction,omitempty" jsonschema:"what Rudolfo said, in his words; empty while it is only a suggestion"`
	Status         string                `json:"status" jsonschema:"suggested (waiting for Rudolfo), active or archived"`
	Rationale      *string               `json:"rationale,omitempty" jsonschema:"for suggestions: the pattern you saw"`
	BasedOn        []string              `json:"based_on,omitempty" jsonschema:"for suggestions: the purchases Rudolfo confirmed that support it"`
	ArchivedReason *string               `json:"archived_reason,omitempty"`
	CreatedAt      string                `json:"created_at"`
	ApprovedAt     string                `json:"approved_at,omitempty"`
}

type RuleConditionsInput struct {
	MinAmount     string `json:"min_amount,omitempty" jsonschema:"only purchases of at least this amount, as a canonical decimal such as 20.00"`
	MaxAmount     string `json:"max_amount,omitempty" jsonschema:"only purchases of at most this amount"`
	Currency      string `json:"currency,omitempty" jsonschema:"ISO 4217 code of the amounts, BRL when omitted"`
	CardLast4     string `json:"card_last4,omitempty" jsonschema:"only purchases with the card ending in these digits"`
	PaymentMethod string `json:"payment_method,omitempty" jsonschema:"only purchases paid with credit_card, debit_card, pix or cash"`
}

type CreateClassificationRuleInput struct {
	Merchant    string `json:"merchant" jsonschema:"store the rule applies to, as Rudolfo named it"`
	Category    string `json:"category" jsonschema:"category code from list_categories"`
	Purpose     string `json:"purpose,omitempty" jsonschema:"purpose that always comes with it, only when Rudolfo said so"`
	Instruction string `json:"instruction" jsonschema:"Rudolfo's words asking for the rule to always hold, in Portuguese"`
	RuleConditionsInput
	RequestID string `json:"request_id" jsonschema:"generated once for this rule and reused on every retry"`
}

type SuggestClassificationRuleInput struct {
	Merchant  string   `json:"merchant" jsonschema:"store of the purchases, as it appears in them"`
	Category  string   `json:"category" jsonschema:"the category Rudolfo confirmed on those purchases"`
	Purpose   string   `json:"purpose,omitempty" jsonschema:"only when Rudolfo gave the same purpose to all of them"`
	Rationale string   `json:"rationale" jsonschema:"the pattern you saw, in Portuguese, to show Rudolfo when asking"`
	BasedOn   []string `json:"based_on" jsonschema:"IDs of 2 to 50 purchases at that store whose category Rudolfo confirmed"`
	RuleConditionsInput
	RequestID string `json:"request_id" jsonschema:"generated once for this suggestion and reused on every retry"`
}

type ApproveClassificationRuleInput struct {
	RuleID      string `json:"rule_id" jsonschema:"the suggestion Rudolfo approved"`
	Instruction string `json:"instruction" jsonschema:"Rudolfo's words approving it, in Portuguese"`
	RequestID   string `json:"request_id" jsonschema:"generated once and reused on every retry"`
}

type ListClassificationRulesInput struct {
	IncludeArchived bool `json:"include_archived,omitempty"`
}

type ArchiveClassificationRuleInput struct {
	RuleID    string `json:"rule_id"`
	Reason    string `json:"reason" jsonschema:"what Rudolfo said, in Portuguese"`
	RequestID string `json:"request_id" jsonschema:"generated once and reused on every retry"`
}

type ClassificationRuleResult struct {
	Outcome Outcome                  `json:"outcome"`
	Rule    ClassificationRuleOutput `json:"rule"`
	Summary string                   `json:"summary"`
}

func (o ClassificationRuleResult) reportedOutcome() Outcome {
	return o.Outcome
}

type ListClassificationRulesOutput struct {
	Rules       []ClassificationRuleOutput `json:"rules" jsonschema:"the rules to apply (and archived ones when asked)"`
	Suggestions []ClassificationRuleOutput `json:"suggestions" jsonschema:"rules you suggested that wait for Rudolfo's answer; never apply them"`
}

func ruleOutput(rule ledger.ClassificationRule) ClassificationRuleOutput {
	out := ClassificationRuleOutput{
		ID:             rule.ID.String(),
		Merchant:       rule.Merchant,
		Category:       CategoryOutput{Code: rule.CategoryCode, Label: rule.CategoryLabel},
		Purpose:        rule.Purpose,
		Instruction:    rule.Instruction,
		Status:         string(rule.Status),
		Rationale:      rule.Rationale,
		BasedOn:        stringsOfIDs(rule.BasedOn),
		ArchivedReason: rule.ArchivedReason,
		CreatedAt:      rule.CreatedAt.UTC().Format(time.RFC3339),
	}
	if rule.ApprovedAt != nil {
		out.ApprovedAt = rule.ApprovedAt.UTC().Format(time.RFC3339)
	}
	if len(out.BasedOn) == 0 {
		out.BasedOn = nil
	}
	c := rule.Conditions
	if c.MinAmount == nil && c.MaxAmount == nil && c.CardLast4 == nil && c.PaymentMethod == nil {
		return out
	}
	out.Conditions = &RuleConditionsOutput{CardLast4: c.CardLast4}
	if c.MinAmount != nil {
		value := moneyOutput(*c.MinAmount)
		out.Conditions.MinAmount = &value
	}
	if c.MaxAmount != nil {
		value := moneyOutput(*c.MaxAmount)
		out.Conditions.MaxAmount = &value
	}
	if c.PaymentMethod != nil {
		method := string(*c.PaymentMethod)
		out.Conditions.PaymentMethod = &method
	}
	return out
}

func conditionsOf(in RuleConditionsInput) (ledger.RuleConditions, error) {
	var conditions ledger.RuleConditions
	currency := strings.TrimSpace(in.Currency)
	if currency == "" {
		currency = "BRL"
	}
	for _, bound := range []struct {
		field  string
		value  string
		target **money.Money
	}{{"min_amount", in.MinAmount, &conditions.MinAmount}, {"max_amount", in.MaxAmount, &conditions.MaxAmount}} {
		if strings.TrimSpace(bound.value) == "" {
			continue
		}
		amount, err := money.Parse(strings.TrimSpace(bound.value), currency)
		if err != nil {
			return conditions, invalid(bound.field, err.Error())
		}
		*bound.target = &amount
	}
	conditions.CardLast4 = optionalString(in.CardLast4)
	if method := optionalString(in.PaymentMethod); method != nil {
		kind := ledger.PaymentKind(*method)
		conditions.PaymentMethod = &kind
	}
	return conditions, nil
}

func ruleSummary(rule ledger.ClassificationRule) string {
	var parts []string
	c := rule.Conditions
	if c.MinAmount != nil {
		parts = append(parts, "a partir de "+c.MinAmount.Format())
	}
	if c.MaxAmount != nil {
		parts = append(parts, "até "+c.MaxAmount.Format())
	}
	if c.PaymentMethod != nil {
		parts = append(parts, "pagas com "+paymentLabel(*c.PaymentMethod))
	}
	if c.CardLast4 != nil {
		parts = append(parts, "no cartão final "+*c.CardLast4)
	}
	text := "compras em " + rule.Merchant
	if len(parts) > 0 {
		text += " (" + strings.Join(parts, ", ") + ")"
	}
	return text + " como " + rule.CategoryLabel
}

func paymentLabel(kind ledger.PaymentKind) string {
	switch kind {
	case ledger.PaymentCreditCard:
		return "cartão de crédito"
	case ledger.PaymentDebitCard:
		return "cartão de débito"
	case ledger.PaymentPix:
		return "Pix"
	default:
		return "dinheiro"
	}
}

func (s *Server) registerRules() {
	addTool(s, toolSpec{
		name:  "create_classification_rule",
		title: "Save a classification rule Rudolfo asked for",
		description: "Saves a rule for future purchases at a store, only when Rudolfo says a guidance must always hold (\"sempre que for na padaria X, é café da manhã\"). A one-off correction is never a rule. " +
			"Rules do not change past purchases; you apply them when classifying, sending category_rule_id.",
		scope:      accounts.ScopeExpensesWrite,
		idempotent: true,
	}, func(ctx context.Context, c call, in CreateClassificationRuleInput) (ClassificationRuleResult, error) {
		conditions, err := conditionsOf(in.RuleConditionsInput)
		if err != nil {
			return ClassificationRuleResult{}, err
		}
		rule, err := s.deps.Ledger.CreateClassificationRule(ctx, c.principal.AccountID, ledger.NewClassificationRule{
			Merchant: in.Merchant, Category: in.Category, Purpose: optionalString(in.Purpose), Conditions: conditions, Instruction: in.Instruction,
		}, c.actor(ledger.AuthorUser, in.RequestID))
		if err != nil {
			return ClassificationRuleResult{}, err
		}
		return ClassificationRuleResult{
			Outcome: OutcomeCompleted,
			Rule:    ruleOutput(rule),
			Summary: "Regra salva: " + ruleSummary(rule) + " daqui em diante.",
		}, nil
	})

	addTool(s, toolSpec{
		name:  "suggest_classification_rule",
		title: "Suggest a classification rule to Rudolfo",
		description: "Records a rule you propose after seeing Rudolfo confirm the same category for purchases at a store, with optional conditions (amount range, card, payment method). " +
			"Lastro checks that every purchase in based_on is at that store, meets the conditions and has that category confirmed by Rudolfo. A suggestion never classifies anything: ask Rudolfo, then approve_classification_rule with his words, or archive_classification_rule when he says no.",
		scope:      accounts.ScopeExpensesWrite,
		idempotent: true,
	}, func(ctx context.Context, c call, in SuggestClassificationRuleInput) (ClassificationRuleResult, error) {
		conditions, err := conditionsOf(in.RuleConditionsInput)
		if err != nil {
			return ClassificationRuleResult{}, err
		}
		basedOn := make([]uuid.UUID, len(in.BasedOn))
		for i, value := range in.BasedOn {
			if basedOn[i], err = parseID(fmt.Sprintf("based_on[%d]", i), value); err != nil {
				return ClassificationRuleResult{}, err
			}
		}
		rule, err := s.deps.Ledger.SuggestClassificationRule(ctx, c.principal.AccountID, ledger.RuleSuggestion{
			Merchant: in.Merchant, Category: in.Category, Purpose: optionalString(in.Purpose), Conditions: conditions, Rationale: in.Rationale, BasedOn: basedOn,
		}, c.actor(ledger.AuthorAgent, in.RequestID))
		if err != nil {
			return ClassificationRuleResult{}, err
		}
		return ClassificationRuleResult{
			Outcome: OutcomeCompleted,
			Rule:    ruleOutput(rule),
			Summary: fmt.Sprintf("Sugestão registrada, ainda sem valer: classificar %s. Pergunte a Rudolfo se ele aprova (%s).", ruleSummary(rule), *rule.Rationale),
		}, nil
	})

	addTool(s, toolSpec{
		name:        "approve_classification_rule",
		title:       "Approve a suggested rule",
		description: "Turns a suggestion into an active rule, only after Rudolfo explicitly said yes. Send his words in instruction.",
		scope:       accounts.ScopeExpensesWrite,
		idempotent:  true,
	}, func(ctx context.Context, c call, in ApproveClassificationRuleInput) (ClassificationRuleResult, error) {
		id, err := parseID("rule_id", in.RuleID)
		if err != nil {
			return ClassificationRuleResult{}, err
		}
		rule, err := s.deps.Ledger.ApproveClassificationRule(ctx, c.principal.AccountID, id, in.Instruction, c.actor(ledger.AuthorUser, in.RequestID))
		if err != nil {
			return ClassificationRuleResult{}, err
		}
		return ClassificationRuleResult{Outcome: OutcomeCompleted, Rule: ruleOutput(rule), Summary: "Regra aprovada: " + ruleSummary(rule) + " daqui em diante."}, nil
	})

	addTool(s, toolSpec{
		name:        "list_classification_rules",
		title:       "List Rudolfo's classification rules",
		description: "Lists the rules Rudolfo asked for or approved, and your suggestions waiting for his answer. Before classifying a purchase, check whether a rule applies to its store and meets its conditions; if it does, use its category and send category_rule_id. Never apply a suggestion.",
		scope:       accounts.ScopeExpensesRead,
		readOnly:    true,
		idempotent:  true,
	}, func(ctx context.Context, c call, in ListClassificationRulesInput) (ListClassificationRulesOutput, error) {
		rules, err := s.deps.Ledger.ClassificationRules(ctx, c.principal.AccountID, in.IncludeArchived)
		if err != nil {
			return ListClassificationRulesOutput{}, err
		}
		out := ListClassificationRulesOutput{Rules: []ClassificationRuleOutput{}, Suggestions: []ClassificationRuleOutput{}}
		for _, rule := range rules {
			if rule.Status == ledger.RuleSuggested {
				out.Suggestions = append(out.Suggestions, ruleOutput(rule))
				continue
			}
			out.Rules = append(out.Rules, ruleOutput(rule))
		}
		return out, nil
	})

	addTool(s, toolSpec{
		name:        "archive_classification_rule",
		title:       "Archive a classification rule",
		description: "Stops a rule when Rudolfo says it no longer holds, or drops a suggestion he refused. Purchases classified by it keep their category; the rule stays listed as archived.",
		scope:       accounts.ScopeExpensesWrite,
		idempotent:  true,
	}, func(ctx context.Context, c call, in ArchiveClassificationRuleInput) (ClassificationRuleResult, error) {
		id, err := parseID("rule_id", in.RuleID)
		if err != nil {
			return ClassificationRuleResult{}, err
		}
		rule, err := s.deps.Ledger.ArchiveClassificationRule(ctx, c.principal.AccountID, id, in.Reason, c.actor(ledger.AuthorUser, in.RequestID))
		if err != nil {
			return ClassificationRuleResult{}, err
		}
		return ClassificationRuleResult{Outcome: OutcomeCompleted, Rule: ruleOutput(rule), Summary: fmt.Sprintf("A regra de %s foi arquivada.", rule.Merchant)}, nil
	})
}
