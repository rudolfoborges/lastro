package mcpserver

import (
	"time"

	"github.com/rudolfoborges/lastro/apps/api/internal/calendar"
	"github.com/rudolfoborges/lastro/apps/api/internal/ledger"
	"github.com/rudolfoborges/lastro/apps/api/internal/money"
)

type MoneyOutput struct {
	Amount    string `json:"amount" jsonschema:"canonical decimal, such as 327.39"`
	Currency  string `json:"currency"`
	Formatted string `json:"formatted" jsonschema:"the amount written for Rudolfo, such as R$ 327,39"`
}

type DateOutput struct {
	At        *string `json:"at,omitempty" jsonschema:"instant in RFC 3339, only when the time is known"`
	On        *string `json:"on,omitempty" jsonschema:"local date in Rudolfo's time zone"`
	Precision string  `json:"precision" jsonschema:"exact, day, month or unknown"`
	Source    string  `json:"source" jsonschema:"stated, inferred or document"`
}

type PaymentMethodOutput struct {
	Kind      string  `json:"kind"`
	CardLast4 *string `json:"card_last4,omitempty"`
}

type InstallmentsOutput struct {
	Count  int          `json:"count"`
	Amount *MoneyOutput `json:"amount,omitempty"`
}

type AllocationOutput struct {
	Category string      `json:"category"`
	Label    string      `json:"label"`
	Amount   MoneyOutput `json:"amount"`
	Status   string      `json:"status" jsonschema:"suggested, confirmed, or missing when no category was given"`
	Origin   string      `json:"origin,omitempty"`
}

type TransactionOutput struct {
	ID                  string               `json:"id"`
	Description         string               `json:"description"`
	OriginalDescription string               `json:"original_description"`
	Merchant            *string              `json:"merchant,omitempty"`
	Payee               *string              `json:"payee,omitempty"`
	Amount              MoneyOutput          `json:"amount"`
	OriginalAmount      *MoneyOutput         `json:"original_amount,omitempty"`
	Date                DateOutput           `json:"date"`
	PaymentMethod       *PaymentMethodOutput `json:"payment_method,omitempty"`
	Nature              string               `json:"nature"`
	Status              string               `json:"status"`
	Purpose             *string              `json:"purpose,omitempty"`
	Installments        *InstallmentsOutput  `json:"installments,omitempty"`
	Categories          []AllocationOutput   `json:"categories,omitempty"`
	CreatedAt           string               `json:"created_at"`
}

type ClarificationOutput struct {
	ID          string             `json:"id"`
	Kind        string             `json:"kind"`
	Question    string             `json:"question" jsonschema:"question to ask Rudolfo, in Portuguese"`
	Status      string             `json:"status"`
	Resolution  *string            `json:"resolution,omitempty"`
	Context     map[string]any     `json:"context,omitempty"`
	Transaction *TransactionOutput `json:"transaction,omitempty"`
	CreatedAt   string             `json:"created_at"`
}

func moneyOutput(m money.Money) MoneyOutput {
	return MoneyOutput{Amount: m.Decimal(), Currency: m.Currency, Formatted: m.Format()}
}

func dateOutput(o calendar.Occurrence) DateOutput {
	out := DateOutput{Precision: string(o.Precision), Source: string(o.Source)}
	if o.At != nil {
		at := o.At.UTC().Format(time.RFC3339)
		out.At = &at
	}
	if o.On != nil {
		on := o.On.String()
		out.On = &on
	}
	return out
}

func transactionOutput(tr ledger.Transaction, allocations []ledger.Allocation) TransactionOutput {
	out := TransactionOutput{
		ID:                  tr.ID.String(),
		Description:         tr.Description,
		OriginalDescription: tr.OriginalDescription,
		Merchant:            tr.Merchant,
		Payee:               tr.Payee,
		Amount:              moneyOutput(tr.Amount),
		Date:                dateOutput(tr.Occurred),
		Nature:              string(tr.Nature),
		Status:              string(tr.Status),
		Purpose:             tr.Purpose,
		CreatedAt:           tr.CreatedAt.UTC().Format(time.RFC3339),
	}
	if tr.OriginalAmount != nil {
		original := moneyOutput(*tr.OriginalAmount)
		out.OriginalAmount = &original
	}
	if tr.PaymentMethod != nil {
		out.PaymentMethod = &PaymentMethodOutput{Kind: string(tr.PaymentMethod.Kind), CardLast4: tr.PaymentMethod.CardLast4}
	}
	if tr.Installments != nil {
		out.Installments = &InstallmentsOutput{Count: tr.Installments.Count}
		if tr.Installments.Amount != nil {
			amount := moneyOutput(*tr.Installments.Amount)
			out.Installments.Amount = &amount
		}
	}
	for _, a := range allocations {
		out.Categories = append(out.Categories, AllocationOutput{Category: a.Code, Label: a.Label, Amount: moneyOutput(a.Amount), Status: string(a.Status), Origin: string(a.Origin)})
	}
	return out
}

func clarificationOutput(c ledger.Clarification) ClarificationOutput {
	out := ClarificationOutput{
		ID:         c.ID.String(),
		Kind:       string(c.Kind),
		Question:   c.Question,
		Status:     string(c.Status),
		Resolution: c.Resolution,
		Context:    c.Context,
		CreatedAt:  c.CreatedAt.UTC().Format(time.RFC3339),
	}
	if len(out.Context) == 0 {
		out.Context = nil
	}
	if c.Transaction != nil {
		transaction := transactionOutput(*c.Transaction, nil)
		out.Transaction = &transaction
	}
	return out
}
