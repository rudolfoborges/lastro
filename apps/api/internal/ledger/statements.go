package ledger

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"

	"github.com/rudolfoborges/lastro/apps/api/internal/calendar"
	"github.com/rudolfoborges/lastro/apps/api/internal/money"
	"github.com/rudolfoborges/lastro/apps/api/internal/redaction"
)

const (
	maxStatementItems  = 500
	maxReadingDoubts   = 50
	maxStatementPages  = 500
	maxExcerpt         = 500
	maxIssuer          = 100
	maxMissing         = 500
	maxItemComponents  = 4
	statementMatchDays = 5
	statementLookback  = 45
)

var ErrStatementVersionUnsupported = errors.New("a different reading of this statement is already recorded; new versions of a statement are not supported yet")

var cardLast4 = regexp.MustCompile(`^[0-9]{4}$`)

type StatementItemKind string

const (
	ItemPurchase       StatementItemKind = "purchase"
	ItemInstallment    StatementItemKind = "installment"
	ItemPixInstallment StatementItemKind = "pix_installment"
	ItemInterest       StatementItemKind = "interest"
	ItemFee            StatementItemKind = "fee"
	ItemTax            StatementItemKind = "tax"
	ItemCredit         StatementItemKind = "credit"
	ItemRefund         StatementItemKind = "refund"
	ItemPayment        StatementItemKind = "payment"
	ItemAdjustment     StatementItemKind = "adjustment"
	ItemUnknown        StatementItemKind = "unknown"
)

var StatementItemKinds = []StatementItemKind{
	ItemPurchase, ItemInstallment, ItemPixInstallment, ItemInterest, ItemFee, ItemTax,
	ItemCredit, ItemRefund, ItemPayment, ItemAdjustment, ItemUnknown,
}

func (k StatementItemKind) installment() bool {
	return k == ItemInstallment || k == ItemPixInstallment
}

type Direction string

const (
	DirectionDebit  Direction = "debit"
	DirectionCredit Direction = "credit"
)

var fixedDirections = map[StatementItemKind]Direction{
	ItemPurchase:       DirectionDebit,
	ItemInstallment:    DirectionDebit,
	ItemPixInstallment: DirectionDebit,
	ItemInterest:       DirectionDebit,
	ItemFee:            DirectionDebit,
	ItemTax:            DirectionDebit,
	ItemCredit:         DirectionCredit,
	ItemRefund:         DirectionCredit,
	ItemPayment:        DirectionCredit,
}

var itemNatures = map[StatementItemKind]Nature{
	ItemPurchase:       NatureExpense,
	ItemInstallment:    NatureExpense,
	ItemPixInstallment: NatureExpense,
	ItemInterest:       NatureCharge,
	ItemFee:            NatureCharge,
	ItemTax:            NatureCharge,
	ItemCredit:         NatureCredit,
	ItemRefund:         NatureRefund,
	ItemPayment:        NaturePayment,
	ItemAdjustment:     NatureAdjustment,
}

type ComponentKind string

const (
	ComponentPrincipal ComponentKind = "principal"
	ComponentInterest  ComponentKind = "interest"
	ComponentIOF       ComponentKind = "iof"
	ComponentFee       ComponentKind = "fee"
)

var ComponentKinds = []ComponentKind{ComponentPrincipal, ComponentInterest, ComponentIOF, ComponentFee}

type Reading string

const (
	ReadingComplete Reading = "complete"
	ReadingPartial  Reading = "partial"
)

type InstallmentPosition struct {
	Number int
	Count  int
}

type ChargeComponent struct {
	Kind     ComponentKind
	Amount   money.Money
	Included bool
}

type StatementItem struct {
	Kind                 StatementItemKind
	Direction            Direction
	OriginalDescription  string
	Description          string
	Merchant             *string
	Amount               money.Money
	OriginalAmount       *money.Money
	Date                 *calendar.Date
	Installment          *InstallmentPosition
	CardLast4            *string
	Page                 int
	Excerpt              string
	Components           []ChargeComponent
	Category             *string
	Purpose              *string
	RelatedTransactionID *uuid.UUID
	RefundOfID           *uuid.UUID
	ConfirmDistinct      bool
	Question             string
}

type ReadingDoubt struct {
	Page     int
	Excerpt  string
	Question string
}

type StatementExtraction struct {
	Issuer          string
	CardLast4       string
	ClosingDate     calendar.Date
	IssueDate       *calendar.Date
	DueDate         calendar.Date
	PeriodStart     *calendar.Date
	PeriodEnd       *calendar.Date
	Total           money.Money
	PreviousBalance *money.Money
	Reading         Reading
	Missing         string
	PageCount       *int
	FileSHA256      []byte
	Items           []StatementItem
	Doubts          []ReadingDoubt
	DryRun          bool
}

type Statement struct {
	ID          uuid.UUID
	Issuer      string
	CardLast4   string
	ClosingDate calendar.Date
	Version     StatementVersion
}

type StatementVersion struct {
	ID              uuid.UUID
	Number          int
	IssueDate       *calendar.Date
	DueDate         calendar.Date
	PeriodStart     *calendar.Date
	PeriodEnd       *calendar.Date
	Total           money.Money
	PreviousBalance *money.Money
	Reading         Reading
	Missing         *string
	PageCount       *int
	CreatedAt       time.Time
}

type StatementResult string

const (
	StatementRecorded        StatementResult = "recorded"
	StatementPreviewed       StatementResult = "previewed"
	StatementAlreadyRecorded StatementResult = "already_recorded"
	StatementPossibleMatch   StatementResult = "possible_match"
)

type StatementLine struct {
	Position      int
	ObservationID uuid.UUID
	Kind          StatementItemKind
	Direction     Direction
	Amount        money.Money
	Page          int
	Excerpt       string
	Effect        InterpretationEffect
	Transaction   *Transaction
	Category      Allocation
	Changed       []Field
	Conflicts     []FieldConflict
}

type ItemCandidates struct {
	Position   int
	Candidates []MatchCandidate
}

type StatementSubmission struct {
	Result     StatementResult
	Statement  *Statement
	Lines      []StatementLine
	Candidates []ItemCandidates
	Review     *StatementReview
	Location   *time.Location
}

type itemIdentity struct {
	base    string
	ordinal int
}

func (i itemIdentity) sourceKey(statementID uuid.UUID) string {
	return fmt.Sprintf("statement:%s:%s:%d", statementID, i.base, i.ordinal)
}

func normalizeExtraction(in StatementExtraction) (StatementExtraction, error) {
	var err error
	if in.Issuer, err = requiredText("issuer", in.Issuer, maxIssuer); err != nil {
		return in, err
	}
	if issuerKey(in.Issuer) == "" {
		return in, &ValidationError{Field: "issuer", Message: "must contain letters or digits"}
	}
	in.CardLast4 = strings.TrimSpace(in.CardLast4)
	if !cardLast4.MatchString(in.CardLast4) {
		return in, &ValidationError{Field: "card_last4", Message: "must be the last 4 digits of the holder's card"}
	}
	if err := validateStatementDates(in); err != nil {
		return in, err
	}
	if err := money.ValidCurrency(in.Total.Currency); err != nil {
		return in, &ValidationError{Field: "currency", Message: err.Error()}
	}
	if in.PreviousBalance != nil && in.PreviousBalance.Currency != in.Total.Currency {
		return in, &ValidationError{Field: "previous_balance", Message: "must be in the statement currency"}
	}
	if in.Missing, err = validateReading(in.Reading, redacted(in.Missing)); err != nil {
		return in, err
	}
	if in.PageCount != nil && (*in.PageCount < 1 || *in.PageCount > maxStatementPages) {
		return in, &ValidationError{Field: "page_count", Message: fmt.Sprintf("must be between 1 and %d", maxStatementPages)}
	}
	if in.FileSHA256 != nil && len(in.FileSHA256) != sha256.Size {
		return in, &ValidationError{Field: "file_sha256", Message: "must be the SHA-256 of the PDF in hexadecimal"}
	}
	if len(in.Items) == 0 || len(in.Items) > maxStatementItems {
		return in, &ValidationError{Field: "items", Message: fmt.Sprintf("must have 1 to %d items, every line of the statement", maxStatementItems)}
	}
	if len(in.Doubts) > maxReadingDoubts {
		return in, &ValidationError{Field: "reading_doubts", Message: fmt.Sprintf("must have at most %d doubts", maxReadingDoubts)}
	}
	for i := range in.Items {
		if in.Items[i], err = normalizeItem(in, in.Items[i]); err != nil {
			return in, prefixed(fmt.Sprintf("items[%d].", i), err)
		}
	}
	if err := validateLinks(in.Items); err != nil {
		return in, err
	}
	for i := range in.Doubts {
		if in.Doubts[i], err = normalizeDoubt(in, in.Doubts[i]); err != nil {
			return in, prefixed(fmt.Sprintf("reading_doubts[%d].", i), err)
		}
	}
	return in, nil
}

func validateStatementDates(in StatementExtraction) error {
	switch {
	case in.ClosingDate == (calendar.Date{}):
		return &ValidationError{Field: "closing_date", Message: "is required; read it from the statement"}
	case in.DueDate == (calendar.Date{}):
		return &ValidationError{Field: "due_date", Message: "is required; read it from the statement"}
	case in.DueDate.Before(in.ClosingDate):
		return &ValidationError{Field: "due_date", Message: "must not be before closing_date"}
	case in.PeriodStart != nil && in.PeriodEnd != nil && in.PeriodEnd.Before(*in.PeriodStart):
		return &ValidationError{Field: "period_end", Message: "must not be before period_start"}
	}
	return nil
}

func validateReading(reading Reading, missing string) (string, error) {
	missing = strings.TrimSpace(missing)
	switch reading {
	case ReadingComplete:
		if missing != "" {
			return "", &ValidationError{Field: "missing", Message: "applies only to a partial reading"}
		}
	case ReadingPartial:
		if missing == "" || len([]rune(missing)) > maxMissing {
			return "", &ValidationError{Field: "missing", Message: fmt.Sprintf("is required for a partial reading: say in Portuguese what was not read, in up to %d characters", maxMissing)}
		}
	default:
		return "", &ValidationError{Field: "reading", Message: "must be complete or partial"}
	}
	return missing, nil
}

func normalizeItem(statement StatementExtraction, item StatementItem) (StatementItem, error) {
	var err error
	if !slices.Contains(StatementItemKinds, item.Kind) {
		return item, &ValidationError{Field: "kind", Message: fmt.Sprintf("must be one of %v", StatementItemKinds)}
	}
	if item.Direction, err = itemDirection(item); err != nil {
		return item, err
	}
	item.OriginalDescription, item.Description, item.Excerpt = redacted(item.OriginalDescription), redacted(item.Description), redacted(item.Excerpt)
	if item.Merchant != nil {
		merchant := redacted(*item.Merchant)
		item.Merchant = &merchant
	}
	if item.OriginalDescription, err = requiredText("original_description", item.OriginalDescription, maxShortText); err != nil {
		return item, err
	}
	item.Description = strings.TrimSpace(item.Description)
	if len([]rune(item.Description)) > maxShortText {
		return item, &ValidationError{Field: "description", Message: fmt.Sprintf("must have at most %d characters", maxShortText)}
	}
	if item.Merchant, err = optionalText("merchant", item.Merchant, maxShortText); err != nil {
		return item, err
	}
	if item.Purpose, err = optionalText("purpose", item.Purpose, maxShortText); err != nil {
		return item, err
	}
	if item.Category != nil {
		code := strings.TrimSpace(*item.Category)
		item.Category = &code
	}
	if err := validateItemAmounts(statement, item); err != nil {
		return item, err
	}
	if item.Date != nil && statement.DueDate.Before(*item.Date) {
		return item, &ValidationError{Field: "date", Message: "must not be after the due date of the statement"}
	}
	if err := validateInstallment(item); err != nil {
		return item, err
	}
	if item.CardLast4 != nil {
		last4 := strings.TrimSpace(*item.CardLast4)
		if !cardLast4.MatchString(last4) {
			return item, &ValidationError{Field: "card_last4", Message: "must be the last 4 digits of the card of this line"}
		}
		item.CardLast4 = &last4
	}
	if err := validateAnchor(statement, item.Page, &item.Excerpt); err != nil {
		return item, err
	}
	if err := validateComponents(item); err != nil {
		return item, err
	}
	return item, validateItemDecisions(&item)
}

func itemDirection(item StatementItem) (Direction, error) {
	fixed, ok := fixedDirections[item.Kind]
	switch {
	case ok && item.Direction != "" && item.Direction != fixed:
		return "", &ValidationError{Field: "direction", Message: fmt.Sprintf("is always %s for %s; leave it out", fixed, item.Kind)}
	case ok:
		return fixed, nil
	case item.Direction != DirectionDebit && item.Direction != DirectionCredit:
		return "", &ValidationError{Field: "direction", Message: fmt.Sprintf("is required for %s: debit when it adds to the statement total, credit when it subtracts", item.Kind)}
	}
	return item.Direction, nil
}

func validateItemAmounts(statement StatementExtraction, item StatementItem) error {
	if item.Amount.Currency != statement.Total.Currency {
		return &ValidationError{Field: "amount", Message: fmt.Sprintf("must be in the statement currency (%s); send the foreign amount in original_amount", statement.Total.Currency)}
	}
	if err := validateAmount("amount", item.Amount); err != nil {
		return err
	}
	if item.OriginalAmount == nil {
		return nil
	}
	if err := validateAmount("original_amount", *item.OriginalAmount); err != nil {
		return err
	}
	if item.OriginalAmount.Currency == statement.Total.Currency {
		return &ValidationError{Field: "original_currency", Message: "must differ from the statement currency; leave original_amount out for domestic lines"}
	}
	return nil
}

func validateInstallment(item StatementItem) error {
	position := item.Installment
	switch {
	case item.Kind.installment() && position == nil:
		return &ValidationError{Field: "installment_number", Message: fmt.Sprintf("and installment_count are required for %s", item.Kind)}
	case position == nil:
		return nil
	case item.Kind == ItemPurchase || item.Kind == ItemPayment:
		return &ValidationError{Field: "installment_number", Message: fmt.Sprintf("does not apply to %s; a line with n/N is an installment", item.Kind)}
	case position.Count < minInstallments || position.Count > maxInstallments:
		return &ValidationError{Field: "installment_count", Message: fmt.Sprintf("must be between %d and %d", minInstallments, maxInstallments)}
	case position.Number < 1 || position.Number > position.Count:
		return &ValidationError{Field: "installment_number", Message: "must be between 1 and installment_count"}
	}
	return nil
}

func validateAnchor(statement StatementExtraction, page int, excerpt *string) error {
	maxPage := maxStatementPages
	if statement.PageCount != nil {
		maxPage = *statement.PageCount
	}
	if page < 1 || page > maxPage {
		return &ValidationError{Field: "page", Message: fmt.Sprintf("is required and must be between 1 and %d: the page where this text is", maxPage)}
	}
	trimmed, err := requiredText("excerpt", *excerpt, maxExcerpt)
	if err != nil {
		return &ValidationError{Field: "excerpt", Message: fmt.Sprintf("is required: copy the text exactly as printed on the page, in up to %d characters", maxExcerpt)}
	}
	*excerpt = trimmed
	return nil
}

func validateComponents(item StatementItem) error {
	if len(item.Components) == 0 {
		return nil
	}
	if item.Direction != DirectionDebit || item.Kind == ItemUnknown {
		return &ValidationError{Field: "components", Message: "apply only to charged items"}
	}
	if len(item.Components) > maxItemComponents {
		return &ValidationError{Field: "components", Message: fmt.Sprintf("must have at most %d entries, one per kind", maxItemComponents)}
	}
	seen := map[ComponentKind]bool{}
	included := int64(0)
	anyIncluded := false
	for i, component := range item.Components {
		field := fmt.Sprintf("components[%d].", i)
		if !slices.Contains(ComponentKinds, component.Kind) {
			return &ValidationError{Field: field + "kind", Message: fmt.Sprintf("must be one of %v", ComponentKinds)}
		}
		if seen[component.Kind] {
			return &ValidationError{Field: field + "kind", Message: "appears twice; send each kind once"}
		}
		seen[component.Kind] = true
		if component.Amount.Currency != item.Amount.Currency {
			return &ValidationError{Field: field + "amount", Message: "must be in the currency of the item"}
		}
		if err := validateAmount(field+"amount", component.Amount); err != nil {
			return err
		}
		if component.Included {
			anyIncluded = true
			included += component.Amount.Minor
		}
	}
	if anyIncluded && included != item.Amount.Minor {
		sum := money.Money{Minor: included, Currency: item.Amount.Currency}
		return &ValidationError{Field: "components", Message: fmt.Sprintf("included in the item add up to %s, not %s; send every included component, principal too, or mark the ones charged apart with included false", sum.Format(), item.Amount.Format())}
	}
	return nil
}

func validateItemDecisions(item *StatementItem) error {
	item.Question = strings.TrimSpace(redacted(item.Question))
	switch {
	case item.RelatedTransactionID != nil && item.ConfirmDistinct:
		return &ValidationError{Field: "confirm_distinct", Message: "cannot be sent with related_transaction_id"}
	case item.ConfirmDistinct && item.Kind != ItemPurchase && item.Kind != ItemRefund:
		return &ValidationError{Field: "confirm_distinct", Message: "applies only to purchase and refund"}
	case item.RefundOfID != nil && item.Kind != ItemRefund:
		return &ValidationError{Field: "refund_of_id", Message: "applies only to refund"}
	case item.RefundOfID != nil && item.RelatedTransactionID != nil:
		return &ValidationError{Field: "refund_of_id", Message: "cannot be sent with related_transaction_id: an existing refund already says which purchase it returns"}
	case item.Kind == ItemUnknown && item.RelatedTransactionID != nil:
		return &ValidationError{Field: "related_transaction_id", Message: "does not apply to unknown, which never changes a transaction"}
	case item.Question != "" && item.Kind != ItemUnknown:
		return &ValidationError{Field: "question", Message: "applies only to unknown"}
	case len([]rune(item.Question)) > maxQuestion:
		return &ValidationError{Field: "question", Message: fmt.Sprintf("must have at most %d characters", maxQuestion)}
	case item.Kind == ItemUnknown && (item.Category != nil || item.Purpose != nil):
		return &ValidationError{Field: "category", Message: "and purpose do not apply to unknown"}
	}
	return nil
}

func validateLinks(items []StatementItem) error {
	linked := map[uuid.UUID]int{}
	for i, item := range items {
		if item.RelatedTransactionID == nil || item.Kind.installment() {
			continue
		}
		if first, ok := linked[*item.RelatedTransactionID]; ok {
			return &ValidationError{Field: fmt.Sprintf("items[%d].related_transaction_id", i), Message: fmt.Sprintf("is also sent in items[%d]; two lines of a statement are two charges", first)}
		}
		linked[*item.RelatedTransactionID] = i
	}
	return nil
}

func normalizeDoubt(statement StatementExtraction, doubt ReadingDoubt) (ReadingDoubt, error) {
	doubt.Excerpt, doubt.Question = redacted(doubt.Excerpt), redacted(doubt.Question)
	if err := validateAnchor(statement, doubt.Page, &doubt.Excerpt); err != nil {
		return doubt, err
	}
	question, err := requiredText("question", doubt.Question, maxQuestion)
	if err != nil {
		return doubt, err
	}
	doubt.Question = question
	return doubt, nil
}

func prefixed(prefix string, err error) error {
	var validation *ValidationError
	if errors.As(err, &validation) {
		return &ValidationError{Field: prefix + validation.Field, Message: validation.Message}
	}
	return err
}

func issuerKey(issuer string) string {
	folded, _, _ := transform.String(transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC), issuer)
	var key strings.Builder
	for _, r := range strings.ToLower(folded) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			key.WriteRune(r)
		}
	}
	return key.String()
}

func itemIdentities(in StatementExtraction) []itemIdentity {
	identities := make([]itemIdentity, len(in.Items))
	seen := map[string]int{}
	for i, item := range in.Items {
		card := in.CardLast4
		if item.CardLast4 != nil {
			card = *item.CardLast4
		}
		parts := []string{normalizedText(item.OriginalDescription), item.Amount.String(), card}
		if item.Date != nil {
			parts = append(parts, item.Date.String())
		}
		if item.Installment != nil {
			parts = append(parts, fmt.Sprintf("%d/%d", item.Installment.Number, item.Installment.Count))
		}
		sum := sha256.Sum256([]byte(strings.Join(parts, "\x1f")))
		base := hex.EncodeToString(sum[:16])
		seen[base]++
		identities[i] = itemIdentity{base: base, ordinal: seen[base]}
	}
	return identities
}

func redacted(value string) string {
	return redaction.Text(value).Text
}

func normalizedText(value string) string {
	return strings.Join(strings.Fields(strings.ToLower(value)), " ")
}

func contentHash(in StatementExtraction, identities []itemIdentity) []byte {
	type line struct {
		Identity       string
		Kind           StatementItemKind
		Direction      Direction
		OriginalAmount *money.Money
	}
	lines := make([]line, len(in.Items))
	for i, item := range in.Items {
		lines[i] = line{Identity: fmt.Sprintf("%s:%d", identities[i].base, identities[i].ordinal), Kind: item.Kind, Direction: item.Direction, OriginalAmount: item.OriginalAmount}
	}
	slices.SortFunc(lines, func(a, b line) int { return strings.Compare(a.Identity, b.Identity) })
	encoded, _ := json.Marshal(struct {
		IssueDate, PeriodStart, PeriodEnd *calendar.Date
		DueDate                           calendar.Date
		Total                             money.Money
		PreviousBalance                   *money.Money
		Reading                           Reading
		Missing                           string
		Lines                             []line
	}{in.IssueDate, in.PeriodStart, in.PeriodEnd, in.DueDate, in.Total, in.PreviousBalance, in.Reading, in.Missing, lines})
	sum := sha256.Sum256(encoded)
	return sum[:]
}
