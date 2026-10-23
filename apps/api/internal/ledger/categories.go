package ledger

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/rudolfoborges/lastro/apps/api/internal/money"
	"github.com/rudolfoborges/lastro/apps/api/internal/store"
	"github.com/rudolfoborges/lastro/apps/api/internal/store/tenantdb"
	"github.com/rudolfoborges/lastro/apps/api/internal/taxonomy"
)

const (
	fieldCategory    = "category"
	maxCategoryLabel = 100
)

var categoryCode = regexp.MustCompile(`^[a-z][a-z0-9_]{0,49}$`)

type AllocationStatus string

const (
	AllocationSuggested AllocationStatus = "suggested"
	AllocationConfirmed AllocationStatus = "confirmed"
	AllocationMissing   AllocationStatus = "missing"
)

type Category struct {
	ID       uuid.UUID
	Code     string
	Label    string
	Archived bool
}

type Allocation struct {
	Code   string
	Label  string
	Amount money.Money
	Status AllocationStatus
	Origin Author
}

type Categorization struct {
	Category Allocation
	Changed  bool
	Conflict *FieldConflict
}

func (s *Service) Categories(ctx context.Context, accountID uuid.UUID) ([]Category, error) {
	var categories []Category
	err := s.inTx(ctx, accountID, func(t *tx) error {
		rows, err := t.q.ListCategories(t.ctx, t.accountID)
		if err != nil {
			return fmt.Errorf("list categories: %w", err)
		}
		categories = make([]Category, len(rows))
		for i, row := range rows {
			categories[i] = toCategory(row)
		}
		return nil
	})
	return categories, err
}

func (s *Service) SaveCategory(ctx context.Context, accountID uuid.UUID, code, label string) (Category, error) {
	if !categoryCode.MatchString(code) {
		return Category{}, &ValidationError{Field: "code", Message: "must be snake_case English, such as pet_care"}
	}
	label, err := requiredText("label", label, maxCategoryLabel)
	if err != nil {
		return Category{}, err
	}
	var category Category
	err = s.inTx(ctx, accountID, func(t *tx) error {
		row, err := t.q.UpdateCategory(t.ctx, tenantdb.UpdateCategoryParams{AccountID: t.accountID, Code: code, Label: label})
		if errors.Is(err, pgx.ErrNoRows) {
			row, err = t.q.InsertCategory(t.ctx, tenantdb.InsertCategoryParams{ID: store.NewID(), AccountID: t.accountID, Code: code, Label: label})
		}
		if err != nil {
			return fmt.Errorf("save category: %w", err)
		}
		category = toCategory(row)
		return nil
	})
	return category, err
}

func (s *Service) ArchiveCategory(ctx context.Context, accountID uuid.UUID, code string) (Category, error) {
	if code == taxonomy.Unidentified {
		return Category{}, &ValidationError{Field: "code", Message: "unidentified is required by the views and cannot be archived"}
	}
	var category Category
	err := s.inTx(ctx, accountID, func(t *tx) error {
		current, err := t.category(code)
		if err != nil {
			return err
		}
		now := time.Now()
		row, err := t.q.UpdateCategory(t.ctx, tenantdb.UpdateCategoryParams{AccountID: t.accountID, Code: code, Label: current.Label, ArchivedAt: &now})
		if err != nil {
			return fmt.Errorf("archive category: %w", err)
		}
		category = toCategory(row)
		return nil
	})
	return category, err
}

func (s *Service) Categorize(ctx context.Context, accountID, transactionID uuid.UUID, code string, status AllocationStatus, reason string, actor Actor) (Categorization, error) {
	var result Categorization
	err := s.inTx(ctx, accountID, func(t *tx) error {
		var err error
		result, err = once(t, "set_category", actor, []any{transactionID, code, status, reason}, func() (Categorization, error) {
			transaction, err := t.lockOpenTransaction(transactionID)
			if err != nil {
				return Categorization{}, err
			}
			result, err := t.categorize(transaction, code, status, reason, actor)
			if err != nil {
				return Categorization{}, err
			}
			return result, t.refreshClarifications(transactionID)
		})
		return err
	})
	return result, err
}

func (s *Service) CategoryOf(ctx context.Context, accountID, transactionID uuid.UUID) (Allocation, error) {
	var allocation Allocation
	err := s.inTx(ctx, accountID, func(t *tx) error {
		var err error
		allocation, err = t.effectiveCategory(transactionID)
		return err
	})
	return allocation, err
}

// EffectiveCategory is what views count: a transaction without a category
// counts in full as "Não identificado".
func EffectiveCategory(transaction Transaction, current *Allocation) Allocation {
	if current != nil {
		return *current
	}
	label := taxonomy.Unidentified
	for _, category := range taxonomy.Defaults {
		if category.Code == taxonomy.Unidentified {
			label = category.Label
		}
	}
	return Allocation{Code: taxonomy.Unidentified, Label: label, Amount: transaction.Amount, Status: AllocationMissing}
}

func (t *tx) categorize(transaction Transaction, code string, status AllocationStatus, reason string, actor Actor) (Categorization, error) {
	reason, err := requiredText("reason", reason, maxReason)
	if err != nil {
		return Categorization{}, err
	}
	origin, err := allocationOrigin(status, actor)
	if err != nil {
		return Categorization{}, err
	}
	category, err := t.category(strings.TrimSpace(code))
	if err != nil {
		return Categorization{}, err
	}
	if category.Archived {
		return Categorization{}, &ValidationError{Field: "category", Message: fmt.Sprintf("category %q is archived", category.Code)}
	}
	planned := Allocation{Code: category.Code, Label: category.Label, Amount: transaction.Amount, Status: status, Origin: origin}
	current, err := t.currentCategory(transaction.ID)
	if err != nil {
		return Categorization{}, err
	}
	if current != nil && current.Code == planned.Code && current.Status == planned.Status {
		return Categorization{Category: *current}, nil
	}
	if actor.Author != AuthorUser && current != nil && current.Status == AllocationConfirmed {
		conflict := FieldConflict{Field: fieldCategory, Current: allocationJSON(current), Proposed: allocationJSON(&planned)}
		if err := t.openConflicts(transaction, []FieldConflict{conflict}, actor); err != nil {
			return Categorization{}, err
		}
		return Categorization{Category: *current, Conflict: &conflict}, nil
	}
	if err := t.replaceCategory(transaction.ID, category, planned); err != nil {
		return Categorization{}, err
	}
	if err := t.recordRevision(transaction.ID, fieldCategory, allocationJSON(current), allocationJSON(&planned), reason, actor); err != nil {
		return Categorization{}, err
	}
	if actor.Author == AuthorUser {
		if err := t.closeConflictsOn(transaction.ID, []Field{fieldCategory}); err != nil {
			return Categorization{}, err
		}
	}
	return Categorization{Category: planned, Changed: true}, nil
}

func (t *tx) followAmount(transaction Transaction) error {
	current, err := t.currentCategory(transaction.ID)
	if err != nil || current == nil {
		return err
	}
	category, err := t.category(current.Code)
	if err != nil {
		return err
	}
	followed := *current
	followed.Amount = transaction.Amount
	return t.replaceCategory(transaction.ID, category, followed)
}

func (t *tx) replaceCategory(transactionID uuid.UUID, category Category, allocation Allocation) error {
	if err := t.q.SupersedeAllocations(t.ctx, tenantdb.SupersedeAllocationsParams{AccountID: t.accountID, TransactionID: transactionID}); err != nil {
		return fmt.Errorf("supersede category: %w", err)
	}
	err := t.q.InsertAllocation(t.ctx, tenantdb.InsertAllocationParams{
		ID:            store.NewID(),
		AccountID:     t.accountID,
		TransactionID: transactionID,
		CategoryID:    category.ID,
		AmountMinor:   allocation.Amount.Minor,
		Status:        string(allocation.Status),
		Origin:        string(allocation.Origin),
	})
	if err != nil {
		return fmt.Errorf("insert category: %w", err)
	}
	return nil
}

func (t *tx) currentCategory(transactionID uuid.UUID) (*Allocation, error) {
	transaction, err := t.q.GetTransaction(t.ctx, tenantdb.GetTransactionParams{AccountID: t.accountID, ID: transactionID})
	if err != nil {
		return nil, fmt.Errorf("transaction %s: %w", transactionID, notFound(err))
	}
	rows, err := t.q.CurrentAllocations(t.ctx, tenantdb.CurrentAllocationsParams{AccountID: t.accountID, TransactionID: transactionID})
	if err != nil {
		return nil, fmt.Errorf("get category: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	row := rows[0]
	return &Allocation{
		Code:   row.Code,
		Label:  row.Label,
		Amount: money.Money{Minor: row.AmountMinor, Currency: transaction.Currency},
		Status: AllocationStatus(row.Status),
		Origin: Author(row.Origin),
	}, nil
}

func (t *tx) effectiveCategory(transactionID uuid.UUID) (Allocation, error) {
	transaction, err := t.transaction(transactionID)
	if err != nil {
		return Allocation{}, err
	}
	current, err := t.currentCategory(transactionID)
	if err != nil {
		return Allocation{}, err
	}
	return EffectiveCategory(transaction, current), nil
}

func (t *tx) category(code string) (Category, error) {
	row, err := t.q.GetCategoryByCode(t.ctx, tenantdb.GetCategoryByCodeParams{AccountID: t.accountID, Code: code})
	if errors.Is(err, pgx.ErrNoRows) {
		return Category{}, t.unknownCategory(code)
	}
	if err != nil {
		return Category{}, fmt.Errorf("get category %s: %w", code, err)
	}
	return toCategory(row), nil
}

func (t *tx) unknownCategory(code string) error {
	rows, err := t.q.ListCategories(t.ctx, t.accountID)
	if err != nil {
		return fmt.Errorf("list categories: %w", err)
	}
	codes := make([]string, 0, len(rows))
	for _, row := range rows {
		if row.ArchivedAt == nil {
			codes = append(codes, row.Code)
		}
	}
	return &ValidationError{Field: "category", Message: fmt.Sprintf("unknown category %q; use one of %s", code, strings.Join(codes, ", "))}
}

func allocationOrigin(status AllocationStatus, actor Actor) (Author, error) {
	switch status {
	case AllocationConfirmed:
		if actor.Author != AuthorUser {
			return "", &ValidationError{Field: "category_status", Message: "only Rudolfo confirms a category; send suggested otherwise"}
		}
		return AuthorUser, nil
	case AllocationSuggested:
		if actor.Author == AuthorSystem {
			return "", &ValidationError{Field: "category_status", Message: "the system does not classify"}
		}
		return actor.Author, nil
	default:
		return "", &ValidationError{Field: "category_status", Message: "must be suggested or confirmed"}
	}
}

func allocationJSON(allocation *Allocation) []byte {
	if allocation == nil {
		return jsonValue(nil)
	}
	return jsonValue(map[string]any{"category": allocation.Code, "status": allocation.Status})
}

func toCategory(row tenantdb.Category) Category {
	return Category{ID: row.ID, Code: row.Code, Label: row.Label, Archived: row.ArchivedAt != nil}
}
