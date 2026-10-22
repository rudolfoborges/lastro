package ledger

import (
	"fmt"

	"github.com/google/uuid"

	"github.com/rudolfoborges/lastro/apps/api/internal/store"
	"github.com/rudolfoborges/lastro/apps/api/internal/store/tenantdb"
)

func (t *tx) ensurePaymentMethod(ref PaymentMethodRef) (PaymentMethod, error) {
	if err := validatePaymentMethod(ref); err != nil {
		return PaymentMethod{}, err
	}
	err := t.q.InsertPaymentMethod(t.ctx, tenantdb.InsertPaymentMethodParams{
		ID:        store.NewID(),
		AccountID: t.accountID,
		Kind:      string(ref.Kind),
		CardLast4: ref.CardLast4,
	})
	if err != nil {
		return PaymentMethod{}, fmt.Errorf("insert payment method: %w", err)
	}
	row, err := t.q.FindPaymentMethod(t.ctx, tenantdb.FindPaymentMethodParams{
		AccountID: t.accountID,
		Kind:      string(ref.Kind),
		CardLast4: ref.CardLast4,
	})
	if err != nil {
		return PaymentMethod{}, fmt.Errorf("find payment method: %w", err)
	}
	return toPaymentMethod(row), nil
}

func (t *tx) paymentMethod(id uuid.UUID) (PaymentMethod, error) {
	row, err := t.q.GetPaymentMethod(t.ctx, tenantdb.GetPaymentMethodParams{AccountID: t.accountID, ID: id})
	if err != nil {
		return PaymentMethod{}, fmt.Errorf("payment method %s: %w", id, notFound(err))
	}
	return toPaymentMethod(row), nil
}

func toPaymentMethod(row tenantdb.PaymentMethod) PaymentMethod {
	return PaymentMethod{
		ID:        row.ID,
		Kind:      PaymentKind(row.Kind),
		CardLast4: row.CardLast4,
		Issuer:    row.Issuer,
		Network:   row.Network,
		Nickname:  row.Nickname,
	}
}
