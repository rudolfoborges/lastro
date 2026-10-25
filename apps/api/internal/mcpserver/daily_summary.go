package mcpserver

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/rudolfoborges/lastro/apps/api/internal/accounts"
	"github.com/rudolfoborges/lastro/apps/api/internal/calendar"
	"github.com/rudolfoborges/lastro/apps/api/internal/ledger"
)

type SetDailySummaryInput struct {
	Enabled bool   `json:"enabled" jsonschema:"true to start sending the daily summary, false to stop"`
	Time    string `json:"time,omitempty" jsonschema:"when to send it every day, HH:MM in Rudolfo's time zone; kept when omitted (08:00 by default)"`
}

type DailySummarySettingsOutput struct {
	Enabled  bool   `json:"enabled"`
	Time     string `json:"time" jsonschema:"when to send it every day, HH:MM"`
	Timezone string `json:"timezone" jsonschema:"Rudolfo's time zone, where time and day are counted"`
}

type SetDailySummaryOutput struct {
	Outcome  Outcome                    `json:"outcome"`
	Settings DailySummarySettingsOutput `json:"settings"`
	Summary  string                     `json:"summary"`
}

func (o SetDailySummaryOutput) reportedOutcome() Outcome {
	return o.Outcome
}

type DailySummaryInput struct {
	Day string `json:"day,omitempty" jsonschema:"YYYY-MM-DD; yesterday in Rudolfo's time zone when omitted"`
}

type DailySummaryOutput struct {
	Settings      DailySummarySettingsOutput `json:"settings"`
	Day           string                     `json:"day,omitempty"`
	Delivered     bool                       `json:"delivered" jsonschema:"true when this day was already sent to Rudolfo; the routine never sends it again"`
	DeliveredAt   string                     `json:"delivered_at,omitempty"`
	Totals        []PurchaseTotalsOutput     `json:"totals,omitempty" jsonschema:"purchases of the day, one entry per currency"`
	Categories    []PurchaseGroupOutput      `json:"categories,omitempty"`
	OpenQuestions int                        `json:"open_questions"`
	Coverage      *CoverageNoteOutput        `json:"coverage,omitempty"`
	Summary       string                     `json:"summary" jsonschema:"the message to send Rudolfo, in Portuguese"`
}

type MarkDailySummaryDeliveredInput struct {
	Day string `json:"day" jsonschema:"the day of the summary that was sent, YYYY-MM-DD, as get_daily_summary returned it"`
}

type MarkDailySummaryDeliveredOutput struct {
	Outcome     Outcome `json:"outcome"`
	Result      string  `json:"result" jsonschema:"marked, or already_delivered when the day had been sent before"`
	Day         string  `json:"day"`
	DeliveredAt string  `json:"delivered_at"`
	Summary     string  `json:"summary"`
}

func (o MarkDailySummaryDeliveredOutput) reportedOutcome() Outcome {
	return o.Outcome
}

func (s *Server) registerDailySummary() {
	addTool(s, toolSpec{
		name:        "set_daily_summary",
		title:       "Turn the daily summary on or off",
		description: "Turns on or off the daily summary of yesterday's purchases that you send Rudolfo, and sets its time. Use only when Rudolfo asks. Lastro never sends messages: you schedule the routine (get_instructions topic daily_summary).",
		scope:       accounts.ScopeExpensesWrite,
		idempotent:  true,
	}, func(ctx context.Context, c call, in SetDailySummaryInput) (SetDailySummaryOutput, error) {
		settings, err := s.deps.Ledger.SetDailySummary(ctx, c.principal.AccountID, in.Enabled, optionalString(in.Time))
		if err != nil {
			return SetDailySummaryOutput{}, err
		}
		out := SetDailySummaryOutput{Outcome: OutcomeCompleted}
		if out.Settings, err = s.dailySummarySettings(ctx, c, settings); err != nil {
			return SetDailySummaryOutput{}, err
		}
		out.Summary = "Resumo diário desativado."
		if settings.Enabled {
			out.Summary = fmt.Sprintf("Resumo diário ativado: todo dia às %s, com as compras do dia anterior.", settings.Time)
		}
		return out, nil
	})

	addTool(s, toolSpec{
		name:  "get_daily_summary",
		title: "Get the daily summary of a day",
		description: "Returns the summary of a finished day (yesterday by default) for you to send Rudolfo: purchases confirmed, provisional and pending apart, by category, open questions and the coverage limitations, with the message ready in Portuguese. " +
			"It says whether the summary is turned on and whether that day was already delivered.",
		scope:      accounts.ScopeExpensesRead,
		readOnly:   true,
		idempotent: true,
	}, s.dailySummary)

	addTool(s, toolSpec{
		name:        "mark_daily_summary_delivered",
		title:       "Record that the daily summary was sent",
		description: "Call right after sending Rudolfo the daily summary of a day, so that day is never sent again.",
		scope:       accounts.ScopeExpensesWrite,
		idempotent:  true,
	}, func(ctx context.Context, c call, in MarkDailySummaryDeliveredInput) (MarkDailySummaryDeliveredOutput, error) {
		day, err := s.finishedDay(ctx, c, in.Day)
		if err != nil {
			return MarkDailySummaryDeliveredOutput{}, err
		}
		delivery, already, err := s.deps.Ledger.MarkDailySummaryDelivered(ctx, c.principal.AccountID, day, c.receivedAt, c.actor(ledger.AuthorAgent, ""))
		if err != nil {
			return MarkDailySummaryDeliveredOutput{}, err
		}
		out := MarkDailySummaryDeliveredOutput{Outcome: OutcomeCompleted, Result: "marked", Day: day.String(), DeliveredAt: delivery.DeliveredAt.UTC().Format(time.RFC3339)}
		out.Summary = fmt.Sprintf("Resumo de %s marcado como entregue.", day.Format())
		if already {
			out.Result = "already_delivered"
			out.Summary = fmt.Sprintf("O resumo de %s já tinha sido entregue; nada mudou.", day.Format())
		}
		return out, nil
	})
}

func (s *Server) dailySummary(ctx context.Context, c call, in DailySummaryInput) (DailySummaryOutput, error) {
	settings, err := s.deps.Ledger.DailySummarySettings(ctx, c.principal.AccountID)
	if err != nil {
		return DailySummaryOutput{}, err
	}
	out := DailySummaryOutput{}
	if out.Settings, err = s.dailySummarySettings(ctx, c, settings); err != nil {
		return DailySummaryOutput{}, err
	}
	if !settings.Enabled {
		out.Summary = "O resumo diário está desativado. Ele só é enviado depois que Rudolfo pedir para ativá-lo."
		return out, nil
	}
	day, err := s.finishedDay(ctx, c, in.Day)
	if err != nil {
		return DailySummaryOutput{}, err
	}
	out.Day = day.String()
	delivery, err := s.deps.Ledger.DailySummaryDelivery(ctx, c.principal.AccountID, day)
	if err != nil {
		return DailySummaryOutput{}, err
	}
	if delivery != nil {
		out.Delivered, out.DeliveredAt = true, delivery.DeliveredAt.UTC().Format(time.RFC3339)
	}
	spending, err := s.querySpending(ctx, c, QuerySpendingInput{From: day.String(), To: day.String(), GroupBy: string(ledger.GroupCategory)})
	if err != nil {
		return DailySummaryOutput{}, err
	}
	out.Totals, out.Categories, out.OpenQuestions, out.Coverage = spending.Totals, spending.Groups, spending.OpenQuestions, &spending.Coverage
	out.Summary = dailySummaryText(spending, day)
	return out, nil
}

func dailySummaryText(spending QuerySpendingOutput, day calendar.Date) string {
	text := purchasesSummary(spending, fmt.Sprintf("Resumo de %s, pela data da compra.", day.Format()))
	if len(spending.Groups) == 0 {
		return text
	}
	parts := make([]string, len(spending.Groups))
	for i, group := range spending.Groups {
		var amounts []string
		for _, total := range group.Totals {
			for _, bucket := range []struct {
				name   string
				amount BucketOutput
			}{{"", total.Confirmed}, {" provisório", total.Provisional}, {" pendente", total.Pending}} {
				if bucket.amount.Count > 0 {
					amounts = append(amounts, bucket.amount.Amount.Formatted+bucket.name)
				}
			}
		}
		parts[i] = group.Label + " " + strings.Join(amounts, " + ")
	}
	return text + " Por categoria: " + strings.Join(parts, "; ") + "."
}

func (s *Server) dailySummarySettings(ctx context.Context, c call, settings ledger.DailySummarySettings) (DailySummarySettingsOutput, error) {
	loc, err := s.deps.Accounts.Location(ctx, c.principal.AccountID)
	if err != nil {
		return DailySummarySettingsOutput{}, err
	}
	return DailySummarySettingsOutput{Enabled: settings.Enabled, Time: settings.Time, Timezone: loc.String()}, nil
}

// finishedDay is yesterday by default; a summary is only ever made of a day
// that is over in Rudolfo's time zone.
func (s *Server) finishedDay(ctx context.Context, c call, value string) (calendar.Date, error) {
	loc, err := s.deps.Accounts.Location(ctx, c.principal.AccountID)
	if err != nil {
		return calendar.Date{}, err
	}
	today := calendar.DateOf(c.receivedAt, loc)
	given, err := parseOptionalDate("day", value)
	if err != nil {
		return calendar.Date{}, err
	}
	if given == nil {
		return today.AddDays(-1), nil
	}
	if !given.Before(today) {
		return calendar.Date{}, invalid("day", "the day is not over yet in Rudolfo's time zone; the summary covers a finished day")
	}
	return *given, nil
}
