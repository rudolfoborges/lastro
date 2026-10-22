package ledger

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
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
	fieldCategory     = "category"
	maxCategoryLabel  = 100
	categoryUnchanged = "Categoria mantida com o novo valor."
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

type Split struct {
	Code       string
	Amount     *money.Money
	AmountText string
}

type Allocation struct {
	Code   string
	Label  string
	Amount money.Money
	Status AllocationStatus
	Origin Author
}

type Categorization struct {
	Allocations []Allocation
	Changed     bool
	Conflict    *FieldConflict
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

func (s *Service) Categorize(ctx context.Context, accountID, transactionID uuid.UUID, split []Split, status AllocationStatus, reason string, actor Actor) (Categorization, error) {
	var result Categorization
	err := s.inTx(ctx, accountID, func(t *tx) error {
		var err error
		result, err = once(t, "set_category", actor, []any{transactionID, split, status, reason}, func() (Categorization, error) {
			transaction, err := t.lockOpenTransaction(transactionID)
			if err != nil {
				return Categorization{}, err
			}
			result, err := t.categorize(transaction, split, status, reason, actor)
			if err != nil {
				return Categorization{}, err
			}
			return result, t.refreshClarifications(transactionID)
		})
		return err
	})
	return result, err
}

func (s *Service) Allocations(ctx context.Context, accountID, transactionID uuid.UUID) ([]Allocation, error) {
	var allocations []Allocation
	err := s.inTx(ctx, accountID, func(t *tx) error {
		transaction, err := t.transaction(transactionID)
		if err != nil {
			return err
		}
		current, err := t.allocations(transactionID)
		if err != nil {
			return err
		}
		allocations = EffectiveAllocations(transaction, current)
		return nil
	})
	return allocations, err
}

// EffectiveAllocations is what views count: a transaction without any
// allocation counts in full as "Não identificado".
func EffectiveAllocations(transaction Transaction, current []Allocation) []Allocation {
	if len(current) > 0 {
		return current
	}
	label := taxonomy.Unidentified
	for _, category := range taxonomy.Defaults {
		if category.Code == taxonomy.Unidentified {
			label = category.Label
		}
	}
	return []Allocation{{Code: taxonomy.Unidentified, Label: label, Amount: transaction.Amount, Status: AllocationMissing}}
}

func (t *tx) categorize(transaction Transaction, split []Split, status AllocationStatus, reason string, actor Actor) (Categorization, error) {
	reason, err := requiredText("reason", reason, maxReason)
	if err != nil {
		return Categorization{}, err
	}
	origin, err := allocationOrigin(status, actor)
	if err != nil {
		return Categorization{}, err
	}
	planned, err := t.planAllocations(transaction, split)
	if err != nil {
		return Categorization{}, err
	}
	current, err := t.allocations(transaction.ID)
	if err != nil {
		return Categorization{}, err
	}
	for i := range planned {
		planned[i].Status, planned[i].Origin = status, origin
	}
	if sameAllocations(current, planned) {
		return Categorization{Allocations: current}, nil
	}
	if actor.Author != AuthorUser && confirmedAllocations(current) {
		conflict := FieldConflict{Field: fieldCategory, Current: allocationsJSON(current), Proposed: allocationsJSON(planned)}
		if err := t.openConflicts(transaction, []FieldConflict{conflict}, actor); err != nil {
			return Categorization{}, err
		}
		return Categorization{Allocations: current, Conflict: &conflict}, nil
	}
	if err := t.replaceAllocations(transaction.ID, planned); err != nil {
		return Categorization{}, err
	}
	if err := t.recordRevision(transaction.ID, fieldCategory, allocationsJSON(current), allocationsJSON(planned), reason, actor); err != nil {
		return Categorization{}, err
	}
	if actor.Author == AuthorUser {
		if err := t.closeConflictsOn(transaction.ID, []Field{fieldCategory}); err != nil {
			return Categorization{}, err
		}
	}
	return Categorization{Allocations: planned, Changed: true}, nil
}

func (t *tx) followAmount(transaction Transaction, split []Split, reason string, actor Actor) error {
	current, err := t.allocations(transaction.ID)
	if err != nil || len(current) == 0 {
		return err
	}
	if split != nil {
		result, err := t.categorize(transaction, split, statusChosenBy(actor), reason, actor)
		if err != nil {
			return err
		}
		if result.Conflict != nil {
			return &ValidationError{Field: "categories", Message: "are confirmed by Rudolfo and can only change with his confirmation"}
		}
		return nil
	}
	if len(current) > 1 {
		return &ValidationError{Field: "categories", Message: "the transaction is split between categories: send the new split together with the new amount"}
	}
	followed := current[0]
	followed.Amount = transaction.Amount
	if err := t.replaceAllocations(transaction.ID, []Allocation{followed}); err != nil {
		return err
	}
	return t.recordRevision(transaction.ID, fieldCategory, allocationsJSON(current), allocationsJSON([]Allocation{followed}), categoryUnchanged, Actor{Author: AuthorSystem})
}

func (t *tx) planAllocations(transaction Transaction, split []Split) ([]Allocation, error) {
	if len(split) == 0 {
		return nil, &ValidationError{Field: "categories", Message: "must name at least one category"}
	}
	split = slices.Clone(split)
	for i, part := range split {
		if part.Amount != nil || part.AmountText == "" {
			continue
		}
		amount, err := money.Parse(part.AmountText, transaction.Amount.Currency)
		if err != nil {
			return nil, &ValidationError{Field: "categories", Message: err.Error()}
		}
		split[i].Amount = &amount
	}
	if len(split) == 1 && split[0].Amount == nil {
		split = []Split{{Code: split[0].Code, Amount: &transaction.Amount}}
	}
	planned := make([]Allocation, 0, len(split))
	total := money.Money{Currency: transaction.Amount.Currency}
	for _, part := range split {
		if part.Amount == nil {
			return nil, &ValidationError{Field: "categories", Message: "every part of a split needs an amount"}
		}
		if part.Amount.Currency != transaction.Amount.Currency || part.Amount.Minor <= 0 {
			return nil, &ValidationError{Field: "categories", Message: fmt.Sprintf("amounts must be positive and in %s", transaction.Amount.Currency)}
		}
		if slices.ContainsFunc(planned, func(a Allocation) bool { return a.Code == part.Code }) {
			return nil, &ValidationError{Field: "categories", Message: fmt.Sprintf("category %q appears more than once", part.Code)}
		}
		category, err := t.category(part.Code)
		if err != nil {
			return nil, err
		}
		if category.Archived {
			return nil, &ValidationError{Field: "categories", Message: fmt.Sprintf("category %q is archived", part.Code)}
		}
		planned = append(planned, Allocation{Code: category.Code, Label: category.Label, Amount: *part.Amount})
		if total, err = total.Add(*part.Amount); err != nil {
			return nil, &ValidationError{Field: "categories", Message: err.Error()}
		}
	}
	if total != transaction.Amount {
		return nil, &ValidationError{Field: "categories", Message: fmt.Sprintf("the split adds up to %s but the transaction is %s; the parts must match it to the cent", total.Format(), transaction.Amount.Format())}
	}
	return planned, nil
}

func (t *tx) replaceAllocations(transactionID uuid.UUID, allocations []Allocation) error {
	if err := t.q.SupersedeAllocations(t.ctx, tenantdb.SupersedeAllocationsParams{AccountID: t.accountID, TransactionID: transactionID}); err != nil {
		return fmt.Errorf("supersede allocations: %w", err)
	}
	for _, allocation := range allocations {
		category, err := t.category(allocation.Code)
		if err != nil {
			return err
		}
		err = t.q.InsertAllocation(t.ctx, tenantdb.InsertAllocationParams{
			ID:            store.NewID(),
			AccountID:     t.accountID,
			TransactionID: transactionID,
			CategoryID:    category.ID,
			AmountMinor:   allocation.Amount.Minor,
			Status:        string(allocation.Status),
			Origin:        string(allocation.Origin),
		})
		if err != nil {
			return fmt.Errorf("insert allocation: %w", err)
		}
	}
	return nil
}

func (t *tx) allocations(transactionID uuid.UUID) ([]Allocation, error) {
	transaction, err := t.q.GetTransaction(t.ctx, tenantdb.GetTransactionParams{AccountID: t.accountID, ID: transactionID})
	if err != nil {
		return nil, fmt.Errorf("transaction %s: %w", transactionID, notFound(err))
	}
	rows, err := t.q.CurrentAllocations(t.ctx, tenantdb.CurrentAllocationsParams{AccountID: t.accountID, TransactionID: transactionID})
	if err != nil {
		return nil, fmt.Errorf("list allocations: %w", err)
	}
	allocations := make([]Allocation, len(rows))
	for i, row := range rows {
		allocations[i] = Allocation{
			Code:   row.Code,
			Label:  row.Label,
			Amount: money.Money{Minor: row.AmountMinor, Currency: transaction.Currency},
			Status: AllocationStatus(row.Status),
			Origin: Author(row.Origin),
		}
	}
	return allocations, nil
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
	return &ValidationError{Field: "categories", Message: fmt.Sprintf("unknown category %q; use one of %s", code, strings.Join(codes, ", "))}
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

func confirmedAllocations(allocations []Allocation) bool {
	return slices.ContainsFunc(allocations, func(a Allocation) bool { return a.Status == AllocationConfirmed })
}

func sameAllocations(a, b []Allocation) bool {
	return slices.EqualFunc(sortedAllocations(a), sortedAllocations(b), func(x, y Allocation) bool {
		return x.Code == y.Code && x.Amount == y.Amount && x.Status == y.Status
	})
}

func sortedAllocations(allocations []Allocation) []Allocation {
	sorted := slices.Clone(allocations)
	slices.SortFunc(sorted, func(x, y Allocation) int { return strings.Compare(x.Code, y.Code) })
	return sorted
}

func allocationsJSON(allocations []Allocation) []byte {
	value := make([]map[string]any, len(allocations))
	for i, a := range sortedAllocations(allocations) {
		value[i] = map[string]any{"category": a.Code, "amount": amountJSON(a.Amount), "status": a.Status}
	}
	return jsonValue(value)
}

func toCategory(row tenantdb.Category) Category {
	return Category{ID: row.ID, Code: row.Code, Label: row.Label, Archived: row.ArchivedAt != nil}
}
