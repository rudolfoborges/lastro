package ledger

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/rudolfoborges/lastro/apps/api/internal/store"
	"github.com/rudolfoborges/lastro/apps/api/internal/store/tenantdb"
)

type RuleStatus string

const (
	RuleActive   RuleStatus = "active"
	RuleArchived RuleStatus = "archived"
)

type ClassificationRule struct {
	ID             uuid.UUID
	Merchant       string
	CategoryCode   string
	CategoryLabel  string
	Purpose        *string
	Instruction    string
	Status         RuleStatus
	ArchivedReason *string
	CreatedAt      time.Time
	ArchivedAt     *time.Time
}

type NewClassificationRule struct {
	Merchant    string
	Category    string
	Purpose     *string
	Instruction string
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

func (t *tx) createRule(in NewClassificationRule) (uuid.UUID, error) {
	merchant, err := requiredText("merchant", in.Merchant, maxShortText)
	if err != nil {
		return uuid.UUID{}, err
	}
	instruction, err := requiredText("instruction", in.Instruction, maxReason)
	if err != nil {
		return uuid.UUID{}, &ValidationError{Field: "instruction", Message: "is required: what Rudolfo said, in his words, asking for the rule to always hold"}
	}
	purpose, err := optionalText("purpose", in.Purpose, maxShortText)
	if err != nil {
		return uuid.UUID{}, err
	}
	key := normalizedName(merchant)
	if key == "" {
		return uuid.UUID{}, &ValidationError{Field: "merchant", Message: "must contain letters or digits"}
	}
	category, err := t.category(in.Category)
	if err != nil {
		return uuid.UUID{}, err
	}
	if category.Archived {
		return uuid.UUID{}, &ValidationError{Field: "category", Message: fmt.Sprintf("category %q is archived", category.Code)}
	}
	row, err := t.q.InsertClassificationRule(t.ctx, tenantdb.InsertClassificationRuleParams{
		ID:          store.NewID(),
		AccountID:   t.accountID,
		Merchant:    merchant,
		MerchantKey: key,
		CategoryID:  category.ID,
		Purpose:     purpose,
		Instruction: instruction,
	})
	if isUniqueViolation(err, "classification_rules_active_idx") {
		return uuid.UUID{}, &ValidationError{Field: "merchant", Message: "already has an active rule; archive it before creating another"}
	}
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("insert classification rule: %w", err)
	}
	return row.ID, nil
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
func (t *tx) applyRule(ruleID *uuid.UUID, category **string, field string) (*ClassificationRule, error) {
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
	if rule.Status != RuleActive {
		return nil, &ValidationError{Field: field, Message: "was archived; classify without it"}
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
	return ClassificationRule{
		ID:             row.ID,
		Merchant:       row.Merchant,
		CategoryCode:   row.CategoryCode,
		CategoryLabel:  row.CategoryLabel,
		Purpose:        row.Purpose,
		Instruction:    row.Instruction,
		Status:         RuleStatus(row.Status),
		ArchivedReason: row.ArchivedReason,
		CreatedAt:      row.CreatedAt,
		ArchivedAt:     row.ArchivedAt,
	}
}
