package ledger

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/rudolfoborges/lastro/apps/api/internal/calendar"
	"github.com/rudolfoborges/lastro/apps/api/internal/money"
	"github.com/rudolfoborges/lastro/apps/api/internal/store"
	"github.com/rudolfoborges/lastro/apps/api/internal/store/tenantdb"
)

const subjectBreakdown = "breakdown"

type PaymentModality string

const (
	ModalityCardInstallment PaymentModality = "card_installment"
	ModalityPixInstallment  PaymentModality = "pix_installment"
)

type InstallmentState string

const (
	InstallmentCharged     InstallmentState = "charged"
	InstallmentKnownFuture InstallmentState = "known_future"
	InstallmentEstimated   InstallmentState = "estimated"
)

type Installment struct {
	Number      int
	Amount      money.Money
	State       InstallmentState
	ClosingDate *calendar.Date
	InformedOn  calendar.Date
}

type ComponentTotal struct {
	Kind   ComponentKind
	Amount money.Money
}

type InstallmentPlan struct {
	ID                     uuid.UUID
	TransactionID          uuid.UUID
	Modality               PaymentModality
	Count                  int
	InstallmentAmount      *money.Money
	InstallmentAmountKnown bool
	Total                  *money.Money
	CardLast4              *string
	InformedOn             calendar.Date
	Installments           []Installment
	Charged                money.Money
	Breakdown              []ComponentTotal
	ChargedWithoutDetail   int
	RemainingKnown         money.Money
	RemainingEstimated     money.Money
}

func (p InstallmentPlan) Remaining() money.Money {
	return money.Money{Minor: p.RemainingKnown.Minor + p.RemainingEstimated.Minor, Currency: p.RemainingKnown.Currency}
}

type FutureInstallment struct {
	Number int
	Amount money.Money
}

func (t *tx) planRow(transactionID uuid.UUID) (*tenantdb.InstallmentPlan, error) {
	row, err := t.q.PlanOfTransaction(t.ctx, tenantdb.PlanOfTransactionParams{AccountID: t.accountID, TransactionID: transactionID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("installment plan: %w", err)
	}
	return &row, nil
}

func (t *tx) installmentPlan(transactionID uuid.UUID) (*InstallmentPlan, error) {
	row, err := t.planRow(transactionID)
	if err != nil || row == nil {
		return nil, err
	}
	rows, err := t.q.ListPlanInstallments(t.ctx, tenantdb.ListPlanInstallmentsParams{AccountID: t.accountID, PlanID: row.ID})
	if err != nil {
		return nil, fmt.Errorf("list installments: %w", err)
	}
	plan := toPlan(*row)
	if err := t.breakdown(&plan); err != nil {
		return nil, err
	}
	known := map[int]Installment{}
	last := 0
	for _, installment := range rows {
		closing := calendar.DateFromTime(installment.ClosingDate)
		entry := Installment{
			Number:      int(installment.Number),
			Amount:      money.Money{Minor: installment.AmountMinor, Currency: row.Currency},
			State:       InstallmentState(installment.State),
			ClosingDate: &closing,
			InformedOn:  calendar.DateFromTime(installment.InformedOn),
		}
		known[entry.Number] = entry
		if entry.State == InstallmentCharged {
			plan.Installments = append(plan.Installments, entry)
			plan.Charged.Minor += entry.Amount.Minor
			last = max(last, entry.Number)
		}
	}
	for number := last + 1; number <= plan.Count; number++ {
		entry, ok := known[number]
		if !ok {
			if entry, ok = plan.estimate(number); !ok {
				continue
			}
		}
		plan.Installments = append(plan.Installments, entry)
		if entry.State == InstallmentKnownFuture {
			plan.RemainingKnown.Minor += entry.Amount.Minor
		} else {
			plan.RemainingEstimated.Minor += entry.Amount.Minor
		}
	}
	return &plan, nil
}

func (t *tx) breakdown(plan *InstallmentPlan) error {
	rows, err := t.q.PlanBreakdown(t.ctx, tenantdb.PlanBreakdownParams{AccountID: t.accountID, PlanID: plan.ID})
	if err != nil {
		return fmt.Errorf("plan breakdown: %w", err)
	}
	for _, row := range rows {
		plan.Breakdown = append(plan.Breakdown, ComponentTotal{Kind: ComponentKind(row.Kind), Amount: money.Money{Minor: row.AmountMinor, Currency: plan.Charged.Currency}})
	}
	without, err := t.q.ChargedWithoutBreakdown(t.ctx, tenantdb.ChargedWithoutBreakdownParams{AccountID: t.accountID, PlanID: plan.ID})
	if err != nil {
		return fmt.Errorf("installments without breakdown: %w", err)
	}
	plan.ChargedWithoutDetail = int(without)
	return nil
}

func toPlan(row tenantdb.InstallmentPlan) InstallmentPlan {
	zero := money.Money{Currency: row.Currency}
	plan := InstallmentPlan{
		ID:                     row.ID,
		TransactionID:          row.TransactionID,
		Modality:               PaymentModality(row.PaymentModality),
		Count:                  int(row.InstallmentCount),
		InstallmentAmountKnown: row.InstallmentAmountKnown,
		CardLast4:              row.CardLast4,
		InformedOn:             calendar.DateFromTime(row.InformedOn),
		Charged:                zero,
		RemainingKnown:         zero,
		RemainingEstimated:     zero,
	}
	if row.InstallmentAmountMinor != nil {
		plan.InstallmentAmount = &money.Money{Minor: *row.InstallmentAmountMinor, Currency: row.Currency}
	}
	if row.TotalMinor != nil {
		plan.Total = &money.Money{Minor: *row.TotalMinor, Currency: row.Currency}
	}
	return plan
}

// estimate never invents a total: without the installment amount or the
// contracted total there is nothing to project.
func (p InstallmentPlan) estimate(number int) (Installment, bool) {
	switch {
	case p.InstallmentAmount != nil:
		state := InstallmentEstimated
		if p.InstallmentAmountKnown {
			state = InstallmentKnownFuture
		}
		return Installment{Number: number, Amount: *p.InstallmentAmount, State: state, InformedOn: p.InformedOn}, true
	case p.Total != nil:
		share := p.Total.Minor / int64(p.Count)
		if number == 1 {
			share += p.Total.Minor - share*int64(p.Count)
		}
		return Installment{Number: number, Amount: money.Money{Minor: share, Currency: p.Total.Currency}, State: InstallmentEstimated, InformedOn: p.InformedOn}, true
	default:
		return Installment{}, false
	}
}

func modalityOf(method *PaymentMethodRef) PaymentModality {
	if method != nil && method.Kind == PaymentPix {
		return ModalityPixInstallment
	}
	return ModalityCardInstallment
}

func (t *tx) syncPlan(transaction Transaction, informedOn calendar.Date, stated bool) error {
	existing, err := t.planRow(transaction.ID)
	if err != nil {
		return err
	}
	if transaction.Installments == nil {
		if existing == nil {
			return nil
		}
		if err := t.assertNoCharges(existing.ID, "installments", "the statement already charged installments of this purchase; correct the count instead"); err != nil {
			return err
		}
		return t.cancelPlan(*existing)
	}
	params := tenantdb.UpdateInstallmentPlanParams{
		AccountID:        t.accountID,
		TransactionID:    transaction.ID,
		PaymentModality:  string(modalityOf(refOf(transaction.PaymentMethod))),
		InstallmentCount: int32(transaction.Installments.Count), //nolint:gosec // bounded by validation
		InformedOn:       informedOn.Time(),
	}
	if transaction.Installments.Amount != nil && (stated || existing == nil) {
		params.InstallmentAmountMinor, params.InstallmentAmountKnown = &transaction.Installments.Amount.Minor, stated
	}
	if transaction.AmountBasis == AmountTotal {
		params.TotalMinor = &transaction.Amount.Minor
	}
	if method := transaction.PaymentMethod; method != nil {
		params.CardLast4 = method.CardLast4
	}
	if existing == nil {
		_, err := t.q.InsertInstallmentPlan(t.ctx, tenantdb.InsertInstallmentPlanParams{
			ID:                     store.NewID(),
			AccountID:              t.accountID,
			TransactionID:          transaction.ID,
			PaymentModality:        params.PaymentModality,
			InstallmentCount:       params.InstallmentCount,
			Currency:               transaction.Amount.Currency,
			InstallmentAmountMinor: params.InstallmentAmountMinor,
			InstallmentAmountKnown: params.InstallmentAmountKnown,
			TotalMinor:             params.TotalMinor,
			CardLast4:              params.CardLast4,
			InformedOn:             params.InformedOn,
		})
		if err != nil {
			return fmt.Errorf("insert installment plan: %w", err)
		}
		return nil
	}
	if err := t.assertCountFits(existing.ID, transaction.Installments.Count); err != nil {
		return err
	}
	params.ID, params.ChainKey = existing.ID, existing.ChainKey
	if params.InstallmentAmountMinor == nil {
		params.InstallmentAmountMinor, params.InstallmentAmountKnown = existing.InstallmentAmountMinor, existing.InstallmentAmountKnown
	}
	if params.CardLast4 == nil {
		params.CardLast4 = existing.CardLast4
	}
	if existing.PaymentModality == string(ModalityPixInstallment) {
		params.PaymentModality = existing.PaymentModality
	}
	if _, err := t.q.UpdateInstallmentPlan(t.ctx, params); err != nil {
		return fmt.Errorf("update installment plan: %w", err)
	}
	return nil
}

func refOf(method *PaymentMethod) *PaymentMethodRef {
	if method == nil {
		return nil
	}
	return &PaymentMethodRef{Kind: method.Kind, CardLast4: method.CardLast4}
}

func (t *tx) cancelPlan(row tenantdb.InstallmentPlan) error {
	params := planUpdate(row)
	now := time.Now()
	params.CanceledAt = &now
	if _, err := t.q.UpdateInstallmentPlan(t.ctx, params); err != nil {
		return fmt.Errorf("cancel installment plan: %w", err)
	}
	return nil
}

func planUpdate(row tenantdb.InstallmentPlan) tenantdb.UpdateInstallmentPlanParams {
	return tenantdb.UpdateInstallmentPlanParams{
		AccountID:              row.AccountID,
		ID:                     row.ID,
		TransactionID:          row.TransactionID,
		PaymentModality:        row.PaymentModality,
		InstallmentCount:       row.InstallmentCount,
		InstallmentAmountMinor: row.InstallmentAmountMinor,
		InstallmentAmountKnown: row.InstallmentAmountKnown,
		TotalMinor:             row.TotalMinor,
		CardLast4:              row.CardLast4,
		ChainKey:               row.ChainKey,
		InformedOn:             row.InformedOn,
		CanceledAt:             row.CanceledAt,
	}
}

func (t *tx) assertNoCharges(planID uuid.UUID, field, message string) error {
	charged, err := t.q.CountChargedInstallments(t.ctx, tenantdb.CountChargedInstallmentsParams{AccountID: t.accountID, PlanID: planID})
	if err != nil {
		return fmt.Errorf("count charged installments: %w", err)
	}
	if charged > 0 {
		return &ValidationError{Field: field, Message: message}
	}
	return nil
}

func (t *tx) assertCountFits(planID uuid.UUID, count int) error {
	rows, err := t.q.ListPlanInstallments(t.ctx, tenantdb.ListPlanInstallmentsParams{AccountID: t.accountID, PlanID: planID})
	if err != nil {
		return fmt.Errorf("list installments: %w", err)
	}
	for _, row := range rows {
		if row.State == string(InstallmentCharged) && int(row.Number) > count {
			return &ValidationError{Field: "installment_count", Message: fmt.Sprintf("installment %d was already charged by a statement", row.Number)}
		}
	}
	return nil
}

func chainKey(item StatementItem) string {
	description := item.OriginalDescription
	if position := item.Installment; position != nil {
		token := regexp.MustCompile(fmt.Sprintf(`(^|\s)0*%d\s*/\s*0*%d(\s|$)`, position.Number, position.Count))
		description = token.ReplaceAllString(description, " ")
	}
	return truncateRunes(normalizedText(description), 300)
}

func (s *statementSubmitter) chainPlans(item StatementItem) ([]tenantdb.InstallmentPlan, error) {
	if item.Installment == nil || item.Installment.Number < 2 {
		return nil, nil
	}
	key, card := chainKey(item), s.lineCard(item)
	plans, err := s.t.q.ChainablePlans(s.t.ctx, tenantdb.ChainablePlansParams{
		AccountID:        s.t.accountID,
		ChainKey:         &key,
		InstallmentCount: int32(item.Installment.Count), //nolint:gosec // bounded by validation
		CardLast4:        &card,
		Currency:         item.Amount.Currency,
		Number:           int32(item.Installment.Number),     //nolint:gosec // bounded by validation
		PreviousNumber:   int32(item.Installment.Number - 1), //nolint:gosec // bounded by validation
		AmountMinor:      item.Amount.Minor,
	})
	if err != nil {
		return nil, fmt.Errorf("find installment plans: %w", err)
	}
	return plans, nil
}

func (s *statementSubmitter) installmentCandidates(item StatementItem) ([]MatchCandidate, error) {
	plans, err := s.chainPlans(item)
	if err != nil {
		return nil, err
	}
	if len(plans) == 1 {
		return nil, nil
	}
	var candidates []MatchCandidate
	for _, plan := range plans {
		transaction, err := s.t.transaction(plan.TransactionID)
		if err != nil {
			return nil, err
		}
		candidate, err := s.t.candidate(transaction, []string{
			fmt.Sprintf("parcela %d/%d do mesmo parcelamento já cobrada com o mesmo valor (%s)", item.Installment.Number-1, item.Installment.Count, item.Amount.Format()),
			"mesmo cartão final " + s.lineCard(item),
			"mesma descrição na fatura",
		})
		if err != nil {
			return nil, err
		}
		candidates = append(candidates, candidate)
	}
	if len(candidates) > 0 {
		return candidates, nil
	}
	return s.purchaseCandidates(item)
}

func (s *statementSubmitter) purchaseCandidates(item StatementItem) ([]MatchCandidate, error) {
	count := int64(item.Installment.Count)
	installmentCount := int32(count) //nolint:gosec // bounded by validation
	from, to := s.window(item)
	rows, err := s.t.q.FindInstallmentCandidates(s.t.ctx, tenantdb.FindInstallmentCandidatesParams{
		AccountID:        s.t.accountID,
		Currency:         item.Amount.Currency,
		PaymentKinds:     []string{string(PaymentCreditCard), string(PaymentPix)},
		InstallmentCount: &installmentCount,
		LowTotal:         item.Amount.Minor*count - count,
		HighTotal:        item.Amount.Minor*count + count,
		FromDay:          from.Time(),
		ToDay:            to.Time(),
		FromAt:           from.Time().In(s.loc),
		ToAt:             to.AddDays(1).Time().In(s.loc),
	})
	if err != nil {
		return nil, fmt.Errorf("find installment candidates: %w", err)
	}
	candidates := make([]MatchCandidate, 0, len(rows))
	for _, row := range rows {
		transaction, err := s.t.withPaymentMethod(row)
		if err != nil {
			return nil, err
		}
		reasons := []string{}
		if transaction.Installments != nil && transaction.Installments.Count == item.Installment.Count {
			reasons = append(reasons, fmt.Sprintf("parcelado em %d vezes", item.Installment.Count))
		}
		if transaction.Amount.Minor >= item.Amount.Minor*count-count && transaction.Amount.Minor <= item.Amount.Minor*count+count {
			reasons = append(reasons, fmt.Sprintf("%d × %s ≈ %s", count, item.Amount.Format(), transaction.Amount.Format()))
		}
		candidate, err := s.t.candidate(transaction, reasons)
		if err != nil {
			return nil, err
		}
		probe := matchProbe{amount: transaction.Amount, merchant: item.Merchant, occurred: itemOccurrence(item), method: &PaymentMethodRef{Kind: PaymentCreditCard}, label: "na fatura"}
		dateReason, dateDifference := compareDates(transaction, probe, s.loc)
		methodReason, methodDifference := compareMethods(transaction.PaymentMethod, probe)
		for _, reason := range []string{dateReason, methodReason} {
			if reason != "" {
				candidate.Reasons = append(candidate.Reasons, reason)
			}
		}
		for _, difference := range []string{dateDifference, methodDifference} {
			if difference != "" {
				candidate.Differences = append(candidate.Differences, difference)
			}
		}
		candidates = append(candidates, candidate)
	}
	return candidates, nil
}

func (s *statementSubmitter) installment(statement tenantdb.Statement, observationID uuid.UUID, item StatementItem, blocking bool) (StatementLine, error) {
	if item.RelatedTransactionID != nil {
		line, err := s.link(observationID, item)
		if err != nil {
			return StatementLine{}, err
		}
		return line, s.charge(statement, line.Transaction.ID, observationID, item)
	}
	if !item.ConfirmDistinct {
		plans, err := s.chainPlans(item)
		if err != nil {
			return StatementLine{}, err
		}
		if len(plans) == 1 {
			chained := item
			chained.RelatedTransactionID = &plans[0].TransactionID
			line, err := s.linkWith(observationID, chained, OriginSystem, "next installment of the same plan, chained by Lastro")
			if err != nil {
				return StatementLine{}, err
			}
			return line, s.charge(statement, plans[0].TransactionID, observationID, item)
		}
	}
	var candidates []MatchCandidate
	if item.ConfirmDistinct || item.DeferMatch || !blocking {
		var err error
		if candidates, err = s.installmentCandidates(item); err != nil {
			return StatementLine{}, err
		}
	}
	line, err := s.createInstallmentPurchase(statement, observationID, item)
	if err != nil {
		return StatementLine{}, err
	}
	if err := s.t.settleCandidates(*line.Transaction, candidates, item.DeferMatch || !blocking, s.actor); err != nil {
		return StatementLine{}, err
	}
	return s.withTransaction(line, line.Transaction.ID)
}

func (s *statementSubmitter) createInstallmentPurchase(statement tenantdb.Statement, observationID uuid.UUID, item StatementItem) (StatementLine, error) {
	description := item.Description
	if description == "" {
		description = item.OriginalDescription
	}
	card := s.lineCard(item)
	amount := item.Amount
	created, err := s.t.createTransaction(observationID, NewTransaction{
		OriginalDescription: item.OriginalDescription,
		Description:         description,
		Merchant:            item.Merchant,
		Amount:              item.Amount,
		AmountBasis:         AmountInstallment,
		OriginalAmount:      item.OriginalAmount,
		Occurred:            itemOccurrence(item),
		PaymentMethod:       &PaymentMethodRef{Kind: PaymentCreditCard, CardLast4: &card},
		Nature:              NatureExpense,
		Status:              StatusConfirmed,
		Purpose:             item.Purpose,
		Installments:        &Installments{Count: item.Installment.Count, Amount: &amount},
	}, s.actor)
	if err != nil {
		return StatementLine{}, err
	}
	if item.Category != nil {
		classifier, reason := classifierOf(s.actor, item.rule)
		if _, err := s.t.categorize(created, *item.Category, AllocationSuggested, reason, classifier); err != nil {
			return StatementLine{}, err
		}
	}
	if err := s.charge(statement, created.ID, observationID, item); err != nil {
		return StatementLine{}, err
	}
	if err := s.t.refreshClarifications(created.ID); err != nil {
		return StatementLine{}, err
	}
	return s.withTransaction(StatementLine{Effect: EffectCreated}, created.ID)
}

func (s *statementSubmitter) charge(statement tenantdb.Statement, transactionID, observationID uuid.UUID, item StatementItem) error {
	plan, err := s.planForLine(transactionID, item, statement)
	if err != nil {
		return err
	}
	if int(plan.InstallmentCount) != item.Installment.Count {
		return &ValidationError{Field: "installment_count", Message: fmt.Sprintf("is %d, but the purchase is in %d installments; link the line to the right purchase or correct the count", item.Installment.Count, plan.InstallmentCount)}
	}
	_, err = s.t.q.UpsertChargedInstallment(s.t.ctx, tenantdb.UpsertChargedInstallmentParams{
		ID:            store.NewID(),
		AccountID:     s.t.accountID,
		PlanID:        plan.ID,
		Number:        int32(item.Installment.Number), //nolint:gosec // bounded by validation
		AmountMinor:   item.Amount.Minor,
		StatementID:   statement.ID,
		ObservationID: &observationID,
		InformedOn:    statement.ClosingDate,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return &ValidationError{Field: "related_transaction_id", Message: fmt.Sprintf("installment %d of this purchase was already charged by another statement", item.Installment.Number)}
	}
	if err != nil {
		return fmt.Errorf("charge installment: %w", err)
	}
	if err := s.askForBreakdown(plan, item); err != nil {
		return err
	}
	for i, future := range item.FutureInstallments {
		if future.Number <= item.Installment.Number || future.Number > item.Installment.Count {
			return &ValidationError{Field: fmt.Sprintf("future_installments[%d].number", i), Message: fmt.Sprintf("must be after %d and up to %d", item.Installment.Number, item.Installment.Count)}
		}
		err := s.t.q.UpsertKnownInstallment(s.t.ctx, tenantdb.UpsertKnownInstallmentParams{
			ID:          store.NewID(),
			AccountID:   s.t.accountID,
			PlanID:      plan.ID,
			Number:      int32(future.Number), //nolint:gosec // bounded above
			AmountMinor: future.Amount.Minor,
			StatementID: statement.ID,
			InformedOn:  statement.ClosingDate,
		})
		if err != nil {
			return fmt.Errorf("record future installment: %w", err)
		}
	}
	return nil
}

func (s *statementSubmitter) planForLine(transactionID uuid.UUID, item StatementItem, statement tenantdb.Statement) (tenantdb.InstallmentPlan, error) {
	key, card := chainKey(item), s.lineCard(item)
	modality := ModalityCardInstallment
	if item.Kind == ItemPixInstallment {
		modality = ModalityPixInstallment
	}
	existing, err := s.t.planRow(transactionID)
	if err != nil {
		return tenantdb.InstallmentPlan{}, err
	}
	if existing == nil {
		transaction, err := s.t.transaction(transactionID)
		if err != nil {
			return tenantdb.InstallmentPlan{}, err
		}
		params := tenantdb.InsertInstallmentPlanParams{
			ID:                     store.NewID(),
			AccountID:              s.t.accountID,
			TransactionID:          transactionID,
			PaymentModality:        string(modality),
			InstallmentCount:       int32(item.Installment.Count), //nolint:gosec // bounded by validation
			Currency:               item.Amount.Currency,
			InstallmentAmountMinor: &item.Amount.Minor,
			CardLast4:              &card,
			ChainKey:               &key,
			InformedOn:             statement.ClosingDate,
		}
		if transaction.AmountBasis == AmountTotal && transaction.Amount.Currency == item.Amount.Currency {
			params.TotalMinor = &transaction.Amount.Minor
		}
		plan, err := s.t.q.InsertInstallmentPlan(s.t.ctx, params)
		if err != nil {
			return tenantdb.InstallmentPlan{}, fmt.Errorf("insert installment plan: %w", err)
		}
		return plan, nil
	}
	params := planUpdate(*existing)
	params.ChainKey, params.CardLast4 = &key, &card
	if item.Kind == ItemPixInstallment {
		params.PaymentModality = string(ModalityPixInstallment)
	}
	if params.InstallmentAmountMinor == nil {
		params.InstallmentAmountMinor = &item.Amount.Minor
	}
	plan, err := s.t.q.UpdateInstallmentPlan(s.t.ctx, params)
	if err != nil {
		return tenantdb.InstallmentPlan{}, fmt.Errorf("update installment plan: %w", err)
	}
	return plan, nil
}

// askForBreakdown asks once per Pix installment plan, never once per cycle,
// and stops asking as soon as a statement details an installment.
func (s *statementSubmitter) askForBreakdown(plan tenantdb.InstallmentPlan, item StatementItem) error {
	if plan.PaymentModality != string(ModalityPixInstallment) {
		return nil
	}
	question := newClarification{kind: ClarificationMissingBreakdown, transactionID: &plan.TransactionID, subject: subjectBreakdown}
	if slices.ContainsFunc(item.Components, func(c ChargeComponent) bool { return c.Included }) {
		return s.t.closeTransactionClarifications(plan.TransactionID, []ClarificationKind{ClarificationMissingBreakdown}, ClarificationResolved, "A fatura passou a detalhar a composição da parcela.")
	}
	transaction, err := s.t.transaction(plan.TransactionID)
	if err != nil {
		return err
	}
	question.question = fmt.Sprintf("A fatura não mostra quanto de cada parcela do Pix parcelado %s é principal, juros, IOF ou tarifa. O valor cobrado foi mantido inteiro. Se você souber a composição, me diga.", describe(transaction))
	question.context = map[string]any{"installment_count": plan.InstallmentCount, "installment_amount": item.Amount.Decimal(), "currency": item.Amount.Currency}
	return s.t.openClarification(question)
}
