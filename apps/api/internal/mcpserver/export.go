package mcpserver

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/rudolfoborges/lastro/apps/api/internal/accounts"
)

type ExportDataOutput struct {
	Outcome     Outcome  `json:"outcome"`
	DownloadURL string   `json:"download_url" jsonschema:"secret link to the zip; give it only to Rudolfo, it works until expires_at"`
	ExpiresAt   string   `json:"expires_at"`
	Contents    []string `json:"contents"`
	Summary     string   `json:"summary"`
}

func (o ExportDataOutput) reportedOutcome() Outcome {
	return o.Outcome
}

func (s *Server) registerExport() {
	addTool(s, toolSpec{
		name:  "export_data",
		title: "Export all of Rudolfo's records",
		description: "Creates a secret download link, valid for 15 minutes, to a zip with every record of Rudolfo's account: CSVs of transactions, categories, installments, statements and statement lines, and the complete JSON (redacted evidence, links, history) that lastro import restores. " +
			"Use it only when Rudolfo asks for his data, and give the link only to him.",
		scope: accounts.ScopeExpensesRead,
	}, func(ctx context.Context, c call, _ NoInput) (ExportDataOutput, error) {
		token, expires, err := s.deps.Exports.NewLink(ctx, c.principal.AccountID, c.receivedAt)
		if err != nil {
			return ExportDataOutput{}, err
		}
		loc, err := s.deps.Accounts.Location(ctx, c.principal.AccountID)
		if err != nil {
			return ExportDataOutput{}, err
		}
		url := strings.TrimRight(s.deps.PublicBaseURL, "/") + "/v1/exports/" + token
		return ExportDataOutput{
			Outcome:     OutcomeCompleted,
			DownloadURL: url,
			ExpiresAt:   expires.UTC().Format(time.RFC3339),
			Contents:    []string{"transactions.csv", "allocations.csv", "installments.csv", "statements.csv", "statement_lines.csv", "lastro-export.json"},
			Summary:     fmt.Sprintf("Sua exportação está pronta: %s (vale até %s). O arquivo traz planilhas CSV dos lançamentos, categorias, parcelas e faturas, e um JSON completo que restaura tudo.", url, expires.In(loc).Format("15:04")),
		}, nil
	})
}
