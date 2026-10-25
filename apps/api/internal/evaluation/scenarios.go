package evaluation

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/rudolfoborges/lastro/apps/api/internal/ledger"
	"github.com/rudolfoborges/lastro/apps/api/internal/money"
	"github.com/rudolfoborges/lastro/apps/api/internal/taxonomy"
)

const (
	ScenarioVersion = 1
	walletPackage   = "com.google.android.apps.walletnfcrel"
	evaluationPhone = "evaluation"
)

type Flow string

const (
	FlowConversation   Flow = "conversation"
	FlowNotifications  Flow = "notifications"
	FlowStatements     Flow = "statements"
	FlowClassification Flow = "classification"
)

var flows = []Flow{FlowConversation, FlowNotifications, FlowStatements, FlowClassification}

type Message struct {
	ID   string
	Text string
}

// Conversation holds what Case must handle as if Rudolfo had sent it, in
// order and in the same conversation.
var Conversation = []Message{
	{ID: "c1", Text: "Gastei R$ 327,39 no mercado agora"},
	{ID: "c2", Text: "Comprei refrigerante e chocolate na conveniência do posto, R$ 18,50 no débito"},
	{ID: "c3", Text: "Abasteci R$ 200 de gasolina no posto agora, no crédito"},
	{ID: "c4", Text: "Comprei umas coisas na Amazon, R$ 89,90 no crédito"},
	{ID: "c5", Text: "Gastei uns 50 ontem"},
	{ID: "c6", Text: "Gastei R$ 327,39 no mercado agora"},
	{ID: "c7", Text: "Fiz um Pix de R$ 150 pro João"},
}

// Statement is the text of a synthetic statement PDF, page by page.
const Statement = `BANCO EXEMPLO - FATURA DO CARTÃO

Página 1
Titular: cartão final 4321
Data de fechamento: 03/10/2026    Vencimento: 10/10/2026
Período: 04/09/2026 a 03/10/2026
Saldo anterior: R$ 0,00
Total desta fatura: R$ 1.167,95

Lançamentos
14/09  SUPERMERCADO EXEMPLO                     152,30
02/07  LOJA DE MOVEIS 03/10                     100,00
10/08  PIX PARCELADO MARIA 02/06                500,00
       Principal 450,00  Juros 50,00
05/09  PIX PARCELADO JOSE 01/03                 300,00
20/09  EXAMPLE APP  USD 20,00                   110,00
20/09  IOF TRANSACAO EXTERIOR                     3,85
22/09  UBER *TRIP                                15,90
22/09  UBER *TRIP                                15,90
25/09  ESTORNO LOJA EXEMPLO                     -30,00

Fim da fatura`

type notification struct {
	key    string
	event  ledger.NotificationEvent
	offset time.Duration
	text   string
}

var notifications = []notification{
	{key: "evaluation|1", event: ledger.NotificationPosted, offset: -2 * time.Hour, text: "Compra aprovada de R$ 32,97 em PADARIA PAO QUENTE com Visa •••• 1234"},
	{key: "evaluation|1", event: ledger.NotificationUpdated, offset: -2*time.Hour + time.Minute, text: "Compra de R$ 32,97 em PADARIA PAO QUENTE com Visa •••• 1234 concluída"},
	{key: "evaluation|2", event: ledger.NotificationPosted, offset: -90 * time.Minute, text: "Pagamento de R$ 80,00 em LOJA X recusado"},
	{key: "evaluation|3", event: ledger.NotificationPosted, offset: -60 * time.Minute, text: "Estorno de R$ 10,00 em LOJA Y no Visa •••• 1234"},
	{key: "evaluation|4", event: ledger.NotificationPosted, offset: -45 * time.Minute, text: "Um novo dispositivo foi adicionado à sua carteira"},
	{key: "evaluation|5", event: ledger.NotificationPosted, offset: -30 * time.Minute, text: "R$ 5"},
}

func prepare(ctx context.Context, l *ledger.Service, sandboxID uuid.UUID, now time.Time) error {
	if _, err := l.SetTracking(ctx, sandboxID, true, nil, now.Add(-24*time.Hour)); err != nil {
		return fmt.Errorf("start sandbox tracking: %w", err)
	}
	for _, n := range notifications {
		_, err := l.IngestNotification(ctx, sandboxID, ledger.WalletNotification{
			DeviceID:        evaluationPhone,
			Package:         walletPackage,
			NotificationKey: n.key,
			Event:           n.event,
			PostedAt:        now.Add(n.offset),
			Title:           "Google Wallet",
			Text:            n.text,
			ReceivedAt:      now.Add(n.offset + 10*time.Second),
		})
		if err != nil {
			return fmt.Errorf("load sandbox notification: %w", err)
		}
	}
	return nil
}

type CheckResult struct {
	Flow   Flow   `json:"flow"`
	Check  string `json:"check"`
	Passed bool   `json:"passed"`
}

type FlowResult struct {
	Flow   Flow `json:"flow"`
	Passed int  `json:"passed"`
	Total  int  `json:"total"`
}

func (f FlowResult) Rate() int {
	if f.Total == 0 {
		return 0
	}
	return f.Passed * 100 / f.Total
}

type Report struct {
	ScenarioVersion int           `json:"scenario_version"`
	Flows           []FlowResult  `json:"flows"`
	Checks          []CheckResult `json:"checks"`
	Criterion8      bool          `json:"criterion_8"`
}

func (r Report) Errors() []CheckResult {
	var failed []CheckResult
	for _, check := range r.Checks {
		if !check.Passed {
			failed = append(failed, check)
		}
	}
	return failed
}

type state struct {
	transactions   []ledger.ListedTransaction
	clarifications []ledger.Clarification
	review         *ledger.StatementReview
	pending        int
	pix            ledger.PixView
	plans          map[uuid.UUID]*ledger.InstallmentPlan
}

func load(ctx context.Context, l *ledger.Service, sandboxID uuid.UUID) (state, error) {
	var st state
	var err error
	if st.transactions, err = l.ListTransactions(ctx, sandboxID, ledger.TransactionFilter{Limit: 200}); err != nil {
		return st, err
	}
	if st.clarifications, err = l.Clarifications(ctx, sandboxID, ledger.ClarificationFilter{Limit: 200}); err != nil {
		return st, err
	}
	if review, err := l.StatementReview(ctx, sandboxID, ledger.StatementRef{}); err == nil {
		st.review = &review
	}
	page, err := l.PendingNotifications(ctx, sandboxID, "", 50)
	if err != nil {
		return st, err
	}
	st.pending = page.Pending
	if st.pix, err = l.PixInstallments(ctx, sandboxID); err != nil {
		return st, err
	}
	st.plans = map[uuid.UUID]*ledger.InstallmentPlan{}
	for _, transaction := range st.transactions {
		if transaction.Installments == nil {
			continue
		}
		detail, err := l.GetTransaction(ctx, sandboxID, transaction.ID)
		if err != nil {
			return st, err
		}
		st.plans[transaction.ID] = detail.Plan
	}
	return st, nil
}

func (st state) with(minor int64, nature ledger.Nature) []ledger.ListedTransaction {
	var found []ledger.ListedTransaction
	for _, transaction := range st.transactions {
		if transaction.Amount.Minor == minor && transaction.Nature == nature {
			found = append(found, transaction)
		}
	}
	return found
}

func (st state) one(minor int64, nature ledger.Nature) *ledger.ListedTransaction {
	found := st.with(minor, nature)
	if len(found) != 1 {
		return nil
	}
	return &found[0]
}

func (st state) open(kind ledger.ClarificationKind, transactionID *uuid.UUID) bool {
	return slices.ContainsFunc(st.clarifications, func(c ledger.Clarification) bool {
		return c.Kind == kind && (transactionID == nil || (c.TransactionID != nil && *c.TransactionID == *transactionID))
	})
}

// handledConversation keeps the checks of what must not be recorded from
// passing on an evaluation where Case recorded nothing at all.
func (st state) handledConversation() bool {
	return len(st.with(32739, ledger.NatureExpense)) > 0
}

func categoryIs(transaction *ledger.ListedTransaction, code string) bool {
	return transaction != nil && transaction.Category.Code == code
}

type check struct {
	flow  Flow
	name  string
	holds func(state) bool
}

var checks = []check{
	{FlowConversation, "c1 e c6: os R$ 327,39 do mercado contados duas vezes na conversa viram um só lançamento", func(st state) bool {
		return len(st.with(32739, ledger.NatureExpense)) == 1
	}},
	{FlowConversation, "c5: o valor aproximado sem o que foi comprado não vira lançamento (a Case pergunta)", func(st state) bool {
		return st.handledConversation() && len(st.with(5000, ledger.NatureExpense)) == 0
	}},
	{FlowConversation, "c2: a compra no débito fica confirmada", func(st state) bool {
		t := st.one(1850, ledger.NatureExpense)
		return t != nil && t.PaymentMethod != nil && t.PaymentMethod.Kind == ledger.PaymentDebitCard && t.Status == ledger.StatusConfirmed
	}},
	{FlowConversation, "c7: o Pix de R$ 150 é registrado e a finalidade fica como dúvida", func(st state) bool {
		t := st.one(15000, ledger.NatureExpense)
		return t != nil && t.PaymentMethod != nil && t.PaymentMethod.Kind == ledger.PaymentPix && t.Purpose == nil && st.open(ledger.ClarificationPurposeUnknown, &t.ID)
	}},
	{FlowNotifications, "n1 e n2: a compra de R$ 32,97 e sua atualização viram um só lançamento no cartão final 1234", func(st state) bool {
		t := st.one(3297, ledger.NatureExpense)
		return t != nil && t.PaymentMethod != nil && t.PaymentMethod.CardLast4 != nil && *t.PaymentMethod.CardLast4 == "1234"
	}},
	{FlowNotifications, "n3: o pagamento recusado não vira despesa", func(st state) bool {
		return st.pending == 0 && len(st.with(8000, ledger.NatureExpense)) == 0
	}},
	{FlowNotifications, "n4: o estorno de R$ 10,00 vira um estorno", func(st state) bool {
		return st.one(1000, ledger.NatureRefund) != nil
	}},
	{FlowNotifications, "n5 e n6: o aviso não vira despesa e o texto incerto abre uma dúvida de leitura", func(st state) bool {
		return len(st.with(500, ledger.NatureExpense)) == 0 && st.open(ledger.ClarificationReadingDoubt, nil)
	}},
	{FlowNotifications, "todas as notificações da caixa de entrada foram interpretadas", func(st state) bool {
		return st.pending == 0
	}},
	{FlowStatements, "a fatura foi registrada e conferida sem diferença", func(st state) bool {
		return st.review != nil && st.review.Status == ledger.ReviewReconciled && st.review.Statement.CardLast4 == "4321"
	}},
	{FlowStatements, "a parcela 03/10 vira compra com plano de 10 parcelas", func(st state) bool {
		for _, plan := range st.plans {
			if plan != nil && plan.Count == 10 && plan.Modality == ledger.ModalityCardInstallment {
				return true
			}
		}
		return false
	}},
	{FlowStatements, "o Pix parcelado detalhado traz R$ 50,00 de juros dentro da parcela", func(st state) bool {
		for _, total := range st.pix.Totals {
			if interest, ok := total.Breakdown[ledger.ComponentInterest]; ok && interest.Minor == 5000 {
				return true
			}
		}
		return false
	}},
	{FlowStatements, "o Pix parcelado sem detalhamento abre a dúvida de composição", func(st state) bool {
		return st.open(ledger.ClarificationMissingBreakdown, nil)
	}},
	{FlowStatements, "a compra em dólar guarda US$ 20,00 como valor original", func(st state) bool {
		t := st.one(11000, ledger.NatureExpense)
		return t != nil && t.OriginalAmount != nil && *t.OriginalAmount == money.Money{Minor: 2000, Currency: "USD"}
	}},
	{FlowStatements, "as duas corridas iguais viram dois lançamentos", func(st state) bool {
		return len(st.with(1590, ledger.NatureExpense)) == 2
	}},
	{FlowStatements, "o estorno da fatura vira um estorno de R$ 30,00", func(st state) bool {
		return st.one(3000, ledger.NatureRefund) != nil
	}},
	{FlowClassification, "c1: mercado é Mercado", func(st state) bool {
		return categoryIs(st.one(32739, ledger.NatureExpense), "groceries")
	}},
	{FlowClassification, "c2: refrigerante e chocolate na conveniência são Conveniência, não refeição, mercado nem combustível", func(st state) bool {
		return categoryIs(st.one(1850, ledger.NatureExpense), "convenience_snacks")
	}},
	{FlowClassification, "c3: gasolina no posto é Combustível", func(st state) bool {
		return categoryIs(st.one(20000, ledger.NatureExpense), "fuel")
	}},
	{FlowClassification, "c4: marketplace sem o produto fica Não identificado", func(st state) bool {
		return categoryIs(st.one(8990, ledger.NatureExpense), taxonomy.Unidentified)
	}},
	{FlowClassification, "fatura: supermercado é Mercado e Uber é Transporte", func(st state) bool {
		trips := st.with(1590, ledger.NatureExpense)
		return categoryIs(st.one(15230, ledger.NatureExpense), "groceries") && len(trips) == 2 &&
			trips[0].Category.Code == "transport" && trips[1].Category.Code == "transport"
	}},
}

var criterion8 = []string{
	"c2: refrigerante e chocolate na conveniência são Conveniência, não refeição, mercado nem combustível",
	"c3: gasolina no posto é Combustível",
}

func grade(ctx context.Context, l *ledger.Service, sandboxID uuid.UUID, _ time.Time) (Report, error) {
	st, err := load(ctx, l, sandboxID)
	if err != nil {
		return Report{}, err
	}
	report := Report{ScenarioVersion: ScenarioVersion, Criterion8: true}
	totals := map[Flow]*FlowResult{}
	for _, flow := range flows {
		totals[flow] = &FlowResult{Flow: flow}
	}
	for _, c := range checks {
		passed := c.holds(st)
		report.Checks = append(report.Checks, CheckResult{Flow: c.flow, Check: c.name, Passed: passed})
		totals[c.flow].Total++
		if passed {
			totals[c.flow].Passed++
		}
		if slices.Contains(criterion8, c.name) && !passed {
			report.Criterion8 = false
		}
	}
	for _, flow := range flows {
		report.Flows = append(report.Flows, *totals[flow])
	}
	return report, nil
}
