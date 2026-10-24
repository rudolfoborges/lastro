package ledger

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"

	"github.com/rudolfoborges/lastro/apps/api/internal/calendar"
	"github.com/rudolfoborges/lastro/apps/api/internal/money"
	"github.com/rudolfoborges/lastro/apps/api/internal/store/tenantdb"
)

const (
	matchWindowDays = 2
	findWindowDays  = 5
)

var sameSourceMessages = map[Source]string{
	SourceConversation:       "already has its own record from the conversation; if Rudolfo told the same purchase twice, void the repeated record instead of linking",
	SourceWalletNotification: "is already confirmed by another Google Wallet notification; two notifications are two purchases unless Rudolfo says otherwise",
	SourceStatementLine:      "is already charged by another statement line; two statement lines are two charges",
}

type MatchCandidate struct {
	Transaction Transaction
	Category    Allocation
	Sources     []Source
	Reasons     []string
	Differences []string
}

type MatchRef struct {
	TransactionID *uuid.UUID
	ObservationID *uuid.UUID
}

type MatchSearch struct {
	Transaction Transaction
	Category    Allocation
	Sources     []Source
	Candidates  []MatchCandidate
}

type matchProbe struct {
	nature          Nature
	amount          money.Money
	originalAmount  *money.Money
	merchant        *string
	occurred        calendar.Occurrence
	method          *PaymentMethodRef
	from, to        calendar.Date
	excludedIDs     []uuid.UUID
	excludedSources []Source
	paymentKinds    []PaymentKind
	label           string
}

func (s *Service) FindMatches(ctx context.Context, accountID uuid.UUID, ref MatchRef) (MatchSearch, error) {
	if (ref.TransactionID == nil) == (ref.ObservationID == nil) {
		return MatchSearch{}, &ValidationError{Field: "transaction_id", Message: "send either transaction_id or observation_id"}
	}
	var search MatchSearch
	err := s.inTx(ctx, accountID, func(t *tx) error {
		loc, err := t.location()
		if err != nil {
			return err
		}
		id, err := t.matchTarget(ref)
		if err != nil {
			return err
		}
		transaction, err := t.transaction(id)
		if err != nil {
			return err
		}
		if transaction.Status == StatusVoided || transaction.Status == StatusMerged {
			return ErrTransactionClosed
		}
		sources, err := t.evidenceSources(id)
		if err != nil {
			return err
		}
		category, err := t.effectiveCategory(id)
		if err != nil {
			return err
		}
		probe := transactionProbe(transaction, sources, loc)
		candidates, err := t.findCandidates(probe, loc)
		search = MatchSearch{Transaction: transaction, Category: category, Sources: sources, Candidates: candidates}
		return err
	})
	return search, err
}

func (t *tx) matchTarget(ref MatchRef) (uuid.UUID, error) {
	if ref.TransactionID != nil {
		return *ref.TransactionID, nil
	}
	id, err := t.q.ConfirmedTransactionOfObservation(t.ctx, tenantdb.ConfirmedTransactionOfObservationParams{AccountID: t.accountID, ObservationID: *ref.ObservationID})
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.UUID{}, &ValidationError{Field: "observation_id", Message: "is not evidence of any transaction; the submission tools already return candidates for evidence not recorded yet"}
	}
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("transaction of observation: %w", err)
	}
	return id, nil
}

func transactionProbe(transaction Transaction, sources []Source, loc *time.Location) matchProbe {
	probe := matchProbe{
		nature:          transaction.Nature,
		amount:          transaction.Amount,
		originalAmount:  transaction.OriginalAmount,
		merchant:        transaction.Merchant,
		occurred:        transaction.Occurred,
		excludedIDs:     []uuid.UUID{transaction.ID},
		excludedSources: sources,
		label:           "no lançamento consultado",
	}
	if method := transaction.PaymentMethod; method != nil {
		probe.method = &PaymentMethodRef{Kind: method.Kind, CardLast4: method.CardLast4}
	}
	probe.paymentKinds = compatibleKinds(probe.method)
	probe.from, probe.to = occurrenceWindow(transaction.Occurred, calendar.DateOf(transaction.CreatedAt, loc), findWindowDays)
	return probe
}

func occurrenceWindow(occurred calendar.Occurrence, fallback calendar.Date, days int) (calendar.Date, calendar.Date) {
	switch {
	case occurred.On == nil:
		return fallback.AddDays(-days), fallback.AddDays(days)
	case occurred.Precision == calendar.PrecisionMonth:
		last := calendar.DateFromTime(occurred.On.Time().AddDate(0, 1, -1))
		return *occurred.On, last
	default:
		return occurred.On.AddDays(-days), occurred.On.AddDays(days)
	}
}

func compatibleKinds(method *PaymentMethodRef) []PaymentKind {
	switch {
	case method == nil:
		return nil
	case method.Kind.IsCard():
		return []PaymentKind{PaymentCreditCard, PaymentDebitCard}
	default:
		return []PaymentKind{method.Kind}
	}
}

func (t *tx) findCandidates(probe matchProbe, loc *time.Location) ([]MatchCandidate, error) {
	params := tenantdb.FindMatchCandidatesParams{
		AccountID:       t.accountID,
		Nature:          string(probe.nature),
		ExcludedIds:     probe.excludedIDs,
		AmountMinor:     probe.amount.Minor,
		Currency:        probe.amount.Currency,
		PaymentKinds:    make([]string, len(probe.paymentKinds)),
		FromDay:         probe.from.Time(),
		ToDay:           probe.to.Time(),
		FromAt:          time.Date(probe.from.Year, probe.from.Month, probe.from.Day, 0, 0, 0, 0, loc),
		ToAt:            time.Date(probe.to.Year, probe.to.Month, probe.to.Day, 0, 0, 0, 0, loc).AddDate(0, 0, 1),
		ExcludedSources: make([]string, len(probe.excludedSources)),
	}
	if params.ExcludedIds == nil {
		params.ExcludedIds = []uuid.UUID{}
	}
	if probe.originalAmount != nil {
		params.OriginalAmountMinor, params.OriginalCurrency = &probe.originalAmount.Minor, &probe.originalAmount.Currency
	}
	for i, kind := range probe.paymentKinds {
		params.PaymentKinds[i] = string(kind)
	}
	for i, source := range probe.excludedSources {
		params.ExcludedSources[i] = string(source)
	}
	rows, err := t.q.FindMatchCandidates(t.ctx, params)
	if err != nil {
		return nil, fmt.Errorf("find match candidates: %w", err)
	}
	candidates := make([]MatchCandidate, 0, len(rows))
	for _, row := range rows {
		transaction, err := t.withPaymentMethod(row)
		if err != nil {
			return nil, err
		}
		candidate, err := t.candidate(transaction, nil)
		if err != nil {
			return nil, err
		}
		candidate.Reasons, candidate.Differences = compare(transaction, probe, loc)
		candidates = append(candidates, candidate)
	}
	return candidates, nil
}

func (t *tx) candidate(transaction Transaction, reasons []string) (MatchCandidate, error) {
	category, err := t.effectiveCategory(transaction.ID)
	if err != nil {
		return MatchCandidate{}, err
	}
	sources, err := t.evidenceSources(transaction.ID)
	if err != nil {
		return MatchCandidate{}, err
	}
	return MatchCandidate{Transaction: transaction, Category: category, Sources: sources, Reasons: reasons}, nil
}

func (t *tx) evidenceSources(transactionID uuid.UUID) ([]Source, error) {
	rows, err := t.q.TransactionEvidenceSources(t.ctx, tenantdb.TransactionEvidenceSourcesParams{AccountID: t.accountID, TransactionID: transactionID})
	if err != nil {
		return nil, fmt.Errorf("list evidence sources: %w", err)
	}
	sources := make([]Source, len(rows))
	for i, row := range rows {
		sources[i] = Source(row)
	}
	return sources, nil
}

func (t *tx) assertDistinctSources(transactionID, observationID uuid.UUID, source Source) error {
	same, err := t.q.HasSameSourceEvidence(t.ctx, tenantdb.HasSameSourceEvidenceParams{AccountID: t.accountID, TransactionID: transactionID, ObservationID: observationID})
	if err != nil {
		return fmt.Errorf("check evidence sources: %w", err)
	}
	if same {
		return &ValidationError{Field: "related_transaction_id", Message: sameSourceMessages[source]}
	}
	return nil
}

func compare(transaction Transaction, probe matchProbe, loc *time.Location) (reasons, differences []string) {
	add := func(reason, difference string) {
		if reason != "" {
			reasons = append(reasons, reason)
		}
		if difference != "" {
			differences = append(differences, difference)
		}
	}
	add(compareAmounts(transaction, probe))
	add(compareDates(transaction, probe, loc))
	add(compareMethods(transaction.PaymentMethod, probe))
	add(compareMerchants(transaction.Merchant, probe))
	return reasons, differences
}

func compareAmounts(transaction Transaction, probe matchProbe) (string, string) {
	sameOriginal := func(a, b *money.Money) bool { return a != nil && b != nil && *a == *b }
	switch {
	case transaction.Amount == probe.amount:
		return "mesmo valor (" + transaction.Amount.Format() + ")", ""
	case sameOriginal(transaction.OriginalAmount, probe.originalAmount):
		return "mesmo valor na moeda original (" + transaction.OriginalAmount.Format() + ")",
			fmt.Sprintf("valor convertido diferente: %s no registro, %s %s", transaction.Amount.Format(), probe.amount.Format(), probe.label)
	case sameOriginal(&transaction.Amount, probe.originalAmount):
		return "mesmo valor na moeda original (" + transaction.Amount.Format() + ")", ""
	case sameOriginal(transaction.OriginalAmount, &probe.amount):
		return "mesmo valor na moeda original (" + probe.amount.Format() + ")", ""
	default:
		return "", fmt.Sprintf("valor diferente: %s no registro, %s %s", transaction.Amount.Format(), probe.amount.Format(), probe.label)
	}
}

func compareDates(transaction Transaction, probe matchProbe, loc *time.Location) (string, string) {
	mine, theirs := transaction.Occurred.On, probe.occurred.On
	switch {
	case mine == nil:
		return "", "registro sem data da compra, criado em " + calendar.DateOf(transaction.CreatedAt, loc).Format()
	case theirs == nil:
		return "", "sem data " + probe.label
	case *mine == *theirs:
		return "mesma data (" + mine.Format() + ")", ""
	}
	days := int(theirs.Time().Sub(mine.Time()).Hours() / 24)
	return "", fmt.Sprintf("data: %s no registro, %s %s (%s)", mine.Format(), theirs.Format(), probe.label, dayGap(days))
}

func dayGap(days int) string {
	switch {
	case days == 1:
		return "1 dia depois"
	case days == -1:
		return "1 dia antes"
	case days > 0:
		return fmt.Sprintf("%d dias depois", days)
	default:
		return fmt.Sprintf("%d dias antes", -days)
	}
}

func compareMethods(mine *PaymentMethod, probe matchProbe) (string, string) {
	theirs := probe.method
	switch {
	case mine == nil && theirs == nil:
		return "", ""
	case mine == nil:
		return "", "registro sem meio de pagamento"
	case theirs == nil:
		return "", "sem meio de pagamento " + probe.label
	case mine.Kind != theirs.Kind:
		return "", fmt.Sprintf("meio de pagamento diferente: %s no registro, %s %s", paymentLabel(mine.Kind), paymentLabel(theirs.Kind), probe.label)
	case mine.CardLast4 != nil && theirs.CardLast4 != nil && *mine.CardLast4 != *theirs.CardLast4:
		return "", fmt.Sprintf("cartão diferente: final %s no registro, final %s %s", *mine.CardLast4, *theirs.CardLast4, probe.label)
	case mine.CardLast4 != nil && theirs.CardLast4 != nil:
		return "mesmo cartão final " + *mine.CardLast4, ""
	default:
		return "mesmo meio de pagamento (" + paymentLabel(mine.Kind) + ")", ""
	}
}

func paymentLabel(kind PaymentKind) string {
	switch kind {
	case PaymentCreditCard:
		return "cartão de crédito"
	case PaymentDebitCard:
		return "cartão de débito"
	case PaymentPix:
		return "Pix"
	case PaymentCash:
		return "dinheiro"
	default:
		return string(kind)
	}
}

func compareMerchants(mine *string, probe matchProbe) (string, string) {
	theirs := probe.merchant
	if mine == nil || theirs == nil {
		return "", ""
	}
	if normalizedName(*mine) == normalizedName(*theirs) {
		return "mesmo estabelecimento", ""
	}
	return "", fmt.Sprintf("nome diferente: \"%s\" no registro, \"%s\" %s", *mine, *theirs, probe.label)
}

func normalizedName(value string) string {
	folded, _, _ := transform.String(transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC), value)
	words := strings.FieldsFunc(strings.ToLower(folded), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	return strings.Join(words, " ")
}
