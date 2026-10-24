package ledger

import (
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/rudolfoborges/lastro/apps/api/internal/calendar"
	"github.com/rudolfoborges/lastro/apps/api/internal/money"
	"github.com/rudolfoborges/lastro/apps/api/internal/redaction"
)

var (
	ErrNotFound             = errors.New("not found")
	ErrDuplicateObservation = errors.New("an observation with the same source identity already exists")
	ErrObservationClaimed   = errors.New("the observation is already confirmed as evidence of another transaction")
	ErrEvidenceClosed       = errors.New("the evidence link is no longer open")
)

type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return e.Field + " " + e.Message
}

type Source string

const (
	SourceConversation       Source = "conversation"
	SourceWalletNotification Source = "wallet_notification"
	SourceStatementLine      Source = "statement_line"
	SourceManual             Source = "manual"
	SourceImport             Source = "import"
)

type EventType string

const (
	EventPurchase       EventType = "purchase"
	EventPurchaseUpdate EventType = "purchase_update"
	EventCancellation   EventType = "cancellation"
	EventRefund         EventType = "refund"
	EventDeclined       EventType = "declined"
	EventNotice         EventType = "notice"
	EventUnrelated      EventType = "unrelated"
	EventUnknown        EventType = "unknown"
)

type Author string

const (
	AuthorAgent  Author = "agent"
	AuthorUser   Author = "user"
	AuthorRule   Author = "rule"
	AuthorSystem Author = "system"
)

type Nature string

const (
	NatureExpense    Nature = "expense"
	NatureCharge     Nature = "charge"
	NatureCredit     Nature = "credit"
	NatureRefund     Nature = "refund"
	NaturePayment    Nature = "payment"
	NatureTransfer   Nature = "transfer"
	NatureAdjustment Nature = "adjustment"
)

var natures = []Nature{NatureExpense, NatureCharge, NatureCredit, NatureRefund, NaturePayment, NatureTransfer, NatureAdjustment}

type Status string

const (
	StatusProvisional          Status = "provisional"
	StatusConfirmed            Status = "confirmed"
	StatusPendingClarification Status = "pending_clarification"
	StatusVoided               Status = "voided"
	StatusMerged               Status = "merged"
)

type PaymentKind string

const (
	PaymentCreditCard PaymentKind = "credit_card"
	PaymentDebitCard  PaymentKind = "debit_card"
	PaymentPix        PaymentKind = "pix"
	PaymentCash       PaymentKind = "cash"
)

var paymentKinds = []PaymentKind{PaymentCreditCard, PaymentDebitCard, PaymentPix, PaymentCash}

func (k PaymentKind) IsCard() bool {
	return k == PaymentCreditCard || k == PaymentDebitCard
}

type AmountBasis string

const (
	AmountTotal       AmountBasis = "total"
	AmountInstallment AmountBasis = "installment"
)

type EvidenceStatus string

const (
	EvidenceSuggested EvidenceStatus = "suggested"
	EvidenceConfirmed EvidenceStatus = "confirmed"
	EvidenceRejected  EvidenceStatus = "rejected"
	EvidenceUndone    EvidenceStatus = "undone"
)

type Origin string

const (
	OriginSystem Origin = "system"
	OriginAgent  Origin = "agent"
	OriginUser   Origin = "user"
)

type Field string

const (
	FieldDescription   Field = "description"
	FieldMerchant      Field = "merchant"
	FieldPayee         Field = "payee"
	FieldAmount        Field = "amount"
	FieldOccurred      Field = "occurred"
	FieldPaymentMethod Field = "payment_method"
	FieldNature        Field = "nature"
	FieldPurpose       Field = "purpose"
	FieldInstallments  Field = "installments"
)

type PaymentMethodRef struct {
	Kind      PaymentKind
	CardLast4 *string
}

type PaymentMethod struct {
	ID        uuid.UUID
	Kind      PaymentKind
	CardLast4 *string
	Issuer    *string
	Network   *string
	Nickname  *string
}

type Installments struct {
	Count  int
	Amount *money.Money
}

type Observation struct {
	ID         uuid.UUID
	Source     Source
	SourceKey  string
	Payload    map[string]any
	Redactions []redaction.Finding
	ReceivedAt time.Time
	CreatedAt  time.Time
}

type NewObservation struct {
	Source      Source
	SourceKey   string
	Payload     map[string]any
	Redactions  []redaction.Finding
	Fingerprint []byte
	ReceivedAt  time.Time
}

type Interpretation struct {
	EventType      EventType
	Amount         *money.Money
	Merchant       *string
	Occurred       calendar.Occurrence
	PaymentMethod  *PaymentMethodRef
	Details        map[string]any
	Author         Author
	Effect         *InterpretationEffect
	TransactionID  *uuid.UUID
	submissionHash []byte
}

type Transaction struct {
	ID                  uuid.UUID
	OriginalDescription string
	Description         string
	Merchant            *string
	Payee               *string
	Amount              money.Money
	AmountBasis         AmountBasis
	OriginalAmount      *money.Money
	Occurred            calendar.Occurrence
	PaymentMethod       *PaymentMethod
	Nature              Nature
	Status              Status
	Purpose             *string
	Installments        *Installments
	RefundOfID          *uuid.UUID
	MergedIntoID        *uuid.UUID
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

type NewTransaction struct {
	OriginalDescription string
	Description         string
	Merchant            *string
	Payee               *string
	Amount              money.Money
	AmountBasis         AmountBasis
	OriginalAmount      *money.Money
	Occurred            calendar.Occurrence
	PaymentMethod       *PaymentMethodRef
	Nature              Nature
	Status              Status
	Purpose             *string
	Installments        *Installments
	RefundOfID          *uuid.UUID
}

type Evidence struct {
	ID            uuid.UUID
	TransactionID uuid.UUID
	ObservationID uuid.UUID
	Source        Source
	ReceivedAt    time.Time
	Status        EvidenceStatus
	Origin        Origin
	Score         *int
	Reasons       []string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type FieldSource struct {
	Field           Field
	ObservationID   *uuid.UUID
	ConfirmedByUser bool
}
