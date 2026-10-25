package httpapi

import (
	"net/http"
	"strconv"
	"time"

	"github.com/rudolfoborges/lastro/apps/api/internal/accounts"
	"github.com/rudolfoborges/lastro/apps/api/internal/ledger"
)

type monthTotalJSON struct {
	Month      string               `json:"month"`
	From       string               `json:"from"`
	To         string               `json:"to"`
	InProgress bool                 `json:"in_progress"`
	Gross      tallyJSON            `json:"gross"`
	Coverage   ledger.MonthCoverage `json:"coverage"`
}

type categoryChangeJSON struct {
	Code       string    `json:"code"`
	Label      string    `json:"label"`
	Current    moneyJSON `json:"current"`
	Previous   moneyJSON `json:"previous"`
	Difference moneyJSON `json:"difference"`
}

type unconcludedJSON struct {
	Provisional   int `json:"provisional"`
	Uncategorized int `json:"uncategorized"`
}

type analysisJSON struct {
	GeneratedAt     time.Time            `json:"generated_at"`
	Currency        string               `json:"currency"`
	Basis           string               `json:"basis"`
	Timezone        string               `json:"timezone"`
	HistoryStartsOn *string              `json:"history_starts_on"`
	Months          []monthTotalJSON     `json:"months"`
	SameDays        comparisonJSON       `json:"same_days"`
	Categories      []categoryChangeJSON `json:"categories"`
	Unconcluded     unconcludedJSON      `json:"unconcluded"`
}

func (a *api) handleAnalysis(w http.ResponseWriter, r *http.Request, principal accounts.Principal) {
	query := r.URL.Query()
	months := 0
	if raw := query.Get("months"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			writeErrorMessage(w, http.StatusBadRequest, "invalid_request", "months must be an integer")
			return
		}
		months = parsed
	}
	now := time.Now()
	analysis, err := a.ledger.Analysis(r.Context(), principal.AccountID, months, query.Get("currency"), now)
	if err != nil {
		a.writeLedgerError(w, r, err)
		return
	}
	loc, err := a.accounts.Location(r.Context(), principal.AccountID)
	if err != nil {
		a.writeInternalError(w, r, err)
		return
	}
	body := analysisJSON{
		GeneratedAt: now.UTC(),
		Currency:    analysis.Currency,
		Basis:       purchaseDateBasis,
		Timezone:    loc.String(),
		Months:      make([]monthTotalJSON, len(analysis.Months)),
		SameDays:    toComparisonJSON(analysis.SameDays),
		Categories:  make([]categoryChangeJSON, len(analysis.Categories)),
		Unconcluded: unconcludedJSON{Provisional: analysis.Provisional, Uncategorized: analysis.Uncategorized},
	}
	if analysis.HistoryStartsOn != nil {
		started := analysis.HistoryStartsOn.String()
		body.HistoryStartsOn = &started
	}
	for i, month := range analysis.Months {
		body.Months[i] = monthTotalJSON{
			Month: month.Month, From: month.From.String(), To: month.To.String(), InProgress: month.InProgress,
			Gross: toTallyJSON(month.Gross), Coverage: month.Coverage,
		}
	}
	for i, change := range analysis.Categories {
		body.Categories[i] = categoryChangeJSON{
			Code: change.Code, Label: change.Label,
			Current: toMoneyJSON(change.Current), Previous: toMoneyJSON(change.Previous), Difference: toMoneyJSON(change.Difference),
		}
	}
	writeJSON(w, http.StatusOK, body)
}
