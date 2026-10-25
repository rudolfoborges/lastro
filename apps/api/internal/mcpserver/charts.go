package mcpserver

import (
	"fmt"

	"github.com/rudolfoborges/lastro/apps/api/internal/calendar"
	"github.com/rudolfoborges/lastro/apps/api/internal/ledger"
	"github.com/rudolfoborges/lastro/apps/api/internal/money"
)

const (
	chartBar        = "bar"
	chartStackedBar = "stacked_bar"
)

type ChartPointOutput struct {
	Label string      `json:"label"`
	Value MoneyOutput `json:"value"`
}

type ChartSeriesOutput struct {
	Name   string             `json:"name" jsonschema:"legend, in Portuguese"`
	Points []ChartPointOutput `json:"points" jsonschema:"one per label, in the order of labels"`
	Total  MoneyOutput        `json:"total" jsonschema:"sum of the points; it equals the matching total of the answer"`
}

type ChartOutput struct {
	Kind     string              `json:"kind" jsonschema:"bar (one series) or stacked_bar (series stacked on each label)"`
	Title    string              `json:"title" jsonschema:"in Portuguese; print it on the chart"`
	Period   string              `json:"period" jsonschema:"in Portuguese; print it on the chart"`
	Currency string              `json:"currency" jsonschema:"one chart per currency; currencies are never mixed"`
	Labels   []string            `json:"labels"`
	Series   []ChartSeriesOutput `json:"series"`
	Notes    []string            `json:"notes,omitempty" jsonschema:"what the numbers mean and the coverage limitations, in Portuguese; print them with the chart"`
}

type chartSeries struct {
	name     string
	values   []money.Money
	optional bool
}

// chartOf keeps every value Lastro computed as it is: the chart only lays
// out the answer's numbers, and each series total is their exact sum.
func chartOf(kind, title, period, currency string, labels []string, series []chartSeries, notes []string) (ChartOutput, error) {
	chart := ChartOutput{Kind: kind, Title: title, Period: period, Currency: currency, Labels: labels, Series: []ChartSeriesOutput{}, Notes: notes}
	for _, s := range series {
		total := money.Money{Currency: currency}
		entry := ChartSeriesOutput{Name: s.name, Points: make([]ChartPointOutput, len(labels))}
		for i, label := range labels {
			value := money.Money{Currency: currency}
			if i < len(s.values) && s.values[i].Currency != "" {
				value = s.values[i]
			}
			var err error
			if total, err = total.Add(value); err != nil {
				return ChartOutput{}, fmt.Errorf("chart %s: %w", title, err)
			}
			entry.Points[i] = ChartPointOutput{Label: label, Value: moneyOutput(value)}
		}
		if s.optional && total.IsZero() {
			continue
		}
		entry.Total = moneyOutput(total)
		chart.Series = append(chart.Series, entry)
	}
	if len(chart.Series) > 1 {
		chart.Kind = chartStackedBar
	}
	return chart, nil
}

func periodLabel(from, to calendar.Date) string {
	if from == to {
		return from.Format()
	}
	return from.Format() + " a " + to.Format()
}

var groupNames = map[ledger.GroupBy]string{
	ledger.GroupCategory:      "categoria",
	ledger.GroupMerchant:      "estabelecimento",
	ledger.GroupDay:           "dia",
	ledger.GroupWeek:          "semana",
	ledger.GroupMonth:         "mês",
	ledger.GroupPaymentMethod: "meio de pagamento",
	ledger.GroupModality:      "modalidade",
}

func purchasesCharts(view ledger.PurchasesView, groupBy ledger.GroupBy, from, to calendar.Date, notes []string) ([]ChartOutput, error) {
	title := "Compras"
	if name, ok := groupNames[groupBy]; ok {
		title += " por " + name
	}
	var charts []ChartOutput
	for _, total := range view.Totals {
		labels := []string{"Total"}
		rows := []ledger.PurchaseTotals{total}
		if len(view.Groups) > 0 {
			labels, rows = nil, nil
			for _, group := range view.Groups {
				labels = append(labels, group.Label)
				rows = append(rows, totalsIn(group.Totals, total.Currency))
			}
		}
		series := []chartSeries{{name: "Confirmados"}, {name: "Provisórios", optional: true}, {name: "Pendentes", optional: true}}
		for _, row := range rows {
			series[0].values = append(series[0].values, row.Confirmed.Amount)
			series[1].values = append(series[1].values, row.Provisional.Amount)
			series[2].values = append(series[2].values, row.Pending.Amount)
		}
		chart, err := chartOf(chartBar, title, periodLabel(from, to), total.Currency, labels, series, notes)
		if err != nil {
			return nil, err
		}
		charts = append(charts, chart)
	}
	return charts, nil
}

func totalsIn(totals []ledger.PurchaseTotals, currency string) ledger.PurchaseTotals {
	for _, total := range totals {
		if total.Currency == currency {
			return total
		}
	}
	empty := money.Money{Currency: currency}
	return ledger.PurchaseTotals{Currency: currency, Confirmed: ledger.Bucket{Amount: empty}, Provisional: ledger.Bucket{Amount: empty}, Pending: ledger.Bucket{Amount: empty}}
}

func statementChart(view ledger.StatementChargesView) (ChartOutput, error) {
	statement := view.Review.Statement
	series := chartSeries{name: "Cobrado na fatura"}
	labels := make([]string, len(view.Categories))
	for i, category := range view.Categories {
		labels[i] = category.Label
		series.values = append(series.values, category.Amount)
	}
	notes := []string{"Lançamentos cobrados no ciclo, por categoria; parcelas entram pelo valor da parcela. Pagamentos, créditos, estornos e saldo anterior ficam fora."}
	if statement.Version.Total.Currency != "" {
		notes = append(notes, "Total informado na fatura: "+statement.Version.Total.Format()+".")
	}
	title := "Fatura do cartão final " + statement.CardLast4 + " por categoria"
	return chartOf(chartBar, title, "Fechamento em "+statement.ClosingDate.Format(), statement.Version.Total.Currency, labels, []chartSeries{series}, notes)
}

func commitmentsCharts(view ledger.CommitmentsView) ([]ChartOutput, error) {
	var charts []ChartOutput
	for _, total := range view.Totals {
		var labels []string
		known, estimated := chartSeries{name: "Conhecidas"}, chartSeries{name: "Estimadas", optional: true}
		for _, month := range view.Months {
			if month.Currency != total.Currency {
				continue
			}
			labels = append(labels, monthLabel(month.Month))
			known.values = append(known.values, month.Known)
			estimated.values = append(estimated.values, month.Estimated)
		}
		notes := []string{"Parcelas ainda por vir, pelo mês em que devem ser cobradas. Não são gastos do período: são compromissos futuros.", "Informação de " + view.AsOf.Format() + "."}
		chart, err := chartOf(chartBar, "Parcelas futuras por mês", "A partir de "+view.AsOf.Format(), total.Currency, labels, []chartSeries{known, estimated}, notes)
		if err != nil {
			return nil, err
		}
		charts = append(charts, chart)
	}
	return charts, nil
}
