package mcpserver

import (
	"context"
	"fmt"
	"time"

	"github.com/rudolfoborges/lastro/apps/api/internal/accounts"
	"github.com/rudolfoborges/lastro/apps/api/internal/ledger"
)

type ClassificationRuleOutput struct {
	ID             string         `json:"id" jsonschema:"send it as category_rule_id or rule_id when the rule decided a category"`
	Merchant       string         `json:"merchant" jsonschema:"store the rule is about, as Rudolfo named it"`
	Category       CategoryOutput `json:"category"`
	Purpose        *string        `json:"purpose,omitempty" jsonschema:"purpose Rudolfo attached to the rule, if any"`
	Instruction    string         `json:"instruction" jsonschema:"what Rudolfo said, in his words"`
	Status         string         `json:"status" jsonschema:"active or archived"`
	ArchivedReason *string        `json:"archived_reason,omitempty"`
	CreatedAt      string         `json:"created_at"`
}

type CreateClassificationRuleInput struct {
	Merchant    string `json:"merchant" jsonschema:"store the rule applies to, as Rudolfo named it"`
	Category    string `json:"category" jsonschema:"category code from list_categories"`
	Purpose     string `json:"purpose,omitempty" jsonschema:"purpose that always comes with it, only when Rudolfo said so"`
	Instruction string `json:"instruction" jsonschema:"Rudolfo's words asking for the rule to always hold, in Portuguese"`
	RequestID   string `json:"request_id" jsonschema:"generated once for this rule and reused on every retry"`
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
	Rules []ClassificationRuleOutput `json:"rules"`
}

func ruleOutput(rule ledger.ClassificationRule) ClassificationRuleOutput {
	return ClassificationRuleOutput{
		ID:             rule.ID.String(),
		Merchant:       rule.Merchant,
		Category:       CategoryOutput{Code: rule.CategoryCode, Label: rule.CategoryLabel},
		Purpose:        rule.Purpose,
		Instruction:    rule.Instruction,
		Status:         string(rule.Status),
		ArchivedReason: rule.ArchivedReason,
		CreatedAt:      rule.CreatedAt.UTC().Format(time.RFC3339),
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
		rule, err := s.deps.Ledger.CreateClassificationRule(ctx, c.principal.AccountID, ledger.NewClassificationRule{
			Merchant: in.Merchant, Category: in.Category, Purpose: optionalString(in.Purpose), Instruction: in.Instruction,
		}, c.actor(ledger.AuthorUser, in.RequestID))
		if err != nil {
			return ClassificationRuleResult{}, err
		}
		return ClassificationRuleResult{
			Outcome: OutcomeCompleted,
			Rule:    ruleOutput(rule),
			Summary: fmt.Sprintf("Regra salva: compras em %s serão classificadas como %s daqui em diante.", rule.Merchant, rule.CategoryLabel),
		}, nil
	})

	addTool(s, toolSpec{
		name:        "list_classification_rules",
		title:       "List Rudolfo's classification rules",
		description: "Lists the rules Rudolfo asked for. Before classifying a purchase, check whether one of them applies to its store; if it does, use its category and send category_rule_id.",
		scope:       accounts.ScopeExpensesRead,
		readOnly:    true,
		idempotent:  true,
	}, func(ctx context.Context, c call, in ListClassificationRulesInput) (ListClassificationRulesOutput, error) {
		rules, err := s.deps.Ledger.ClassificationRules(ctx, c.principal.AccountID, in.IncludeArchived)
		if err != nil {
			return ListClassificationRulesOutput{}, err
		}
		out := ListClassificationRulesOutput{Rules: []ClassificationRuleOutput{}}
		for _, rule := range rules {
			out.Rules = append(out.Rules, ruleOutput(rule))
		}
		return out, nil
	})

	addTool(s, toolSpec{
		name:        "archive_classification_rule",
		title:       "Archive a classification rule",
		description: "Stops a rule when Rudolfo says it no longer holds. Purchases classified by it keep their category; the rule stays listed as archived.",
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
