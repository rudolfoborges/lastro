package ledger

import (
	"context"
	"fmt"
	"slices"

	"github.com/google/uuid"

	"github.com/rudolfoborges/lastro/apps/api/internal/calendar"
	"github.com/rudolfoborges/lastro/apps/api/internal/money"
	"github.com/rudolfoborges/lastro/apps/api/internal/store/tenantdb"
)

const (
	defaultTransactions = 50
	maxTransactions     = 200
)

var activeStatuses = []Status{StatusProvisional, StatusConfirmed, StatusPendingClarification}

type TransactionFilter struct {
	From          *calendar.Date
	To            *calendar.Date
	Statuses      []Status
	Category      *string
	PaymentMethod *PaymentKind
	Limit         int
	Offset        int
}

type ListedTransaction struct {
	Transaction
	Categories []Allocation
}

func (s *Service) ListTransactions(ctx context.Context, accountID uuid.UUID, filter TransactionFilter) ([]ListedTransaction, error) {
	params, err := listParams(accountID, filter)
	if err != nil {
		return nil, err
	}
	var listed []ListedTransaction
	err = s.inTx(ctx, accountID, func(t *tx) error {
		rows, err := t.q.ListTransactions(t.ctx, params)
		if err != nil {
			return fmt.Errorf("list transactions: %w", err)
		}
		methods, err := t.paymentMethods()
		if err != nil {
			return err
		}
		ids := make([]uuid.UUID, len(rows))
		for i, row := range rows {
			ids[i] = row.ID
		}
		allocations, err := t.allocationsFor(ids, rows)
		if err != nil {
			return err
		}
		listed = make([]ListedTransaction, len(rows))
		for i, row := range rows {
			var method *PaymentMethod
			if row.PaymentMethodID != nil {
				found := methods[*row.PaymentMethodID]
				method = &found
			}
			transaction := toTransaction(row, method)
			listed[i] = ListedTransaction{Transaction: transaction, Categories: EffectiveAllocations(transaction, allocations[row.ID])}
		}
		return nil
	})
	return listed, err
}

func listParams(accountID uuid.UUID, filter TransactionFilter) (tenantdb.ListTransactionsParams, error) {
	if filter.From != nil && filter.To != nil && filter.To.Before(*filter.From) {
		return tenantdb.ListTransactionsParams{}, &ValidationError{Field: "to", Message: "must not be before from"}
	}
	statuses := filter.Statuses
	if len(statuses) == 0 {
		statuses = activeStatuses
	}
	statusText := make([]string, len(statuses))
	for i, status := range statuses {
		if !slices.Contains([]Status{StatusProvisional, StatusConfirmed, StatusPendingClarification, StatusVoided, StatusMerged}, status) {
			return tenantdb.ListTransactionsParams{}, &ValidationError{Field: "status", Message: "must be provisional, confirmed, pending_clarification, voided or merged"}
		}
		statusText[i] = string(status)
	}
	if filter.PaymentMethod != nil && !slices.Contains(paymentKinds, *filter.PaymentMethod) {
		return tenantdb.ListTransactionsParams{}, &ValidationError{Field: "payment_method", Message: fmt.Sprintf("must be one of %v", paymentKinds)}
	}
	if filter.Offset < 0 {
		return tenantdb.ListTransactionsParams{}, &ValidationError{Field: "offset", Message: "must not be negative"}
	}
	limit := filter.Limit
	if limit <= 0 {
		limit = defaultTransactions
	}
	params := tenantdb.ListTransactionsParams{
		AccountID:    accountID,
		Statuses:     statusText,
		FromDate:     dateTime(filter.From),
		ToDate:       dateTime(filter.To),
		CategoryCode: filter.Category,
		MaxRows:      int32(min(limit, maxTransactions)),   //nolint:gosec // bounded above
		SkipRows:     int32(min(filter.Offset, 1_000_000)), //nolint:gosec // bounded above
	}
	if filter.PaymentMethod != nil {
		kind := string(*filter.PaymentMethod)
		params.PaymentKind = &kind
	}
	return params, nil
}

func (t *tx) paymentMethods() (map[uuid.UUID]PaymentMethod, error) {
	rows, err := t.q.ListPaymentMethods(t.ctx, t.accountID)
	if err != nil {
		return nil, fmt.Errorf("list payment methods: %w", err)
	}
	methods := make(map[uuid.UUID]PaymentMethod, len(rows))
	for _, row := range rows {
		methods[row.ID] = toPaymentMethod(row)
	}
	return methods, nil
}

func (t *tx) allocationsFor(ids []uuid.UUID, transactions []tenantdb.Transaction) (map[uuid.UUID][]Allocation, error) {
	currency := make(map[uuid.UUID]string, len(transactions))
	for _, row := range transactions {
		currency[row.ID] = row.Currency
	}
	rows, err := t.q.AllocationsForTransactions(t.ctx, tenantdb.AllocationsForTransactionsParams{AccountID: t.accountID, TransactionIds: ids})
	if err != nil {
		return nil, fmt.Errorf("list allocations: %w", err)
	}
	allocations := map[uuid.UUID][]Allocation{}
	for _, row := range rows {
		allocations[row.TransactionID] = append(allocations[row.TransactionID], Allocation{
			Code:   row.Code,
			Label:  row.Label,
			Amount: money.Money{Minor: row.AmountMinor, Currency: currency[row.TransactionID]},
			Status: AllocationStatus(row.Status),
			Origin: Author(row.Origin),
		})
	}
	return allocations, nil
}
