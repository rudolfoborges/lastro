package mcpserver

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/rudolfoborges/lastro/apps/api/internal/accounts"
	"github.com/rudolfoborges/lastro/apps/api/internal/calendar"
	"github.com/rudolfoborges/lastro/apps/api/internal/ledger"
	"github.com/rudolfoborges/lastro/apps/api/internal/money"
)

type StatementComponentInput struct {
	Kind     string `json:"kind" jsonschema:"principal, interest, iof or fee"`
	Amount   string `json:"amount" jsonschema:"canonical decimal in the statement currency"`
	Included bool   `json:"included" jsonschema:"true when this amount is part of the item amount; false when it is charged apart in another line"`
}

type FutureInstallmentInput struct {
	Number int    `json:"number" jsonschema:"installment number, after the one charged on this line"`
	Amount string `json:"amount" jsonschema:"amount printed for it, canonical decimal in the statement currency"`
}

type StatementItemInput struct {
	Kind                 string                    `json:"kind" jsonschema:"what the line is; previous balance goes in previous_balance, not here"`
	OriginalDescription  string                    `json:"original_description" jsonschema:"the description exactly as printed on the line"`
	Description          string                    `json:"description,omitempty" jsonschema:"short readable description in Portuguese; the original description when omitted"`
	Merchant             string                    `json:"merchant,omitempty" jsonschema:"store or payee, only when the line shows it"`
	Amount               string                    `json:"amount" jsonschema:"amount of the line in the statement currency, as a positive canonical decimal such as 32.97; kind says whether it adds or subtracts"`
	Direction            string                    `json:"direction,omitempty" jsonschema:"only for adjustment and unknown: debit when the line adds to the total, credit when it subtracts"`
	OriginalAmount       string                    `json:"original_amount,omitempty" jsonschema:"amount in the foreign currency, for international lines"`
	OriginalCurrency     string                    `json:"original_currency,omitempty" jsonschema:"ISO 4217 code of original_amount"`
	Date                 string                    `json:"date,omitempty" jsonschema:"date printed on the line, YYYY-MM-DD; take the year from the statement cycle"`
	InstallmentNumber    int                       `json:"installment_number,omitempty" jsonschema:"n of n/N; required for installment and pix_installment"`
	InstallmentCount     int                       `json:"installment_count,omitempty" jsonschema:"N of n/N"`
	CardLast4            string                    `json:"card_last4,omitempty" jsonschema:"last 4 digits of the card of this line when it is not the holder's (additional or virtual card)"`
	Page                 int                       `json:"page" jsonschema:"page of the PDF where the line is printed, starting at 1"`
	Excerpt              string                    `json:"excerpt" jsonschema:"the text of the line exactly as printed, so Rudolfo can find it in his PDF"`
	Components           []StatementComponentInput `json:"components,omitempty" jsonschema:"principal, interest, IOF and fee of the line, only when the statement details them"`
	FutureInstallments   []FutureInstallmentInput  `json:"future_installments,omitempty" jsonschema:"only when the statement prints the next installments of this same purchase; never project them yourself"`
	Category             string                    `json:"category,omitempty" jsonschema:"suggested category code from list_categories"`
	Purpose              string                    `json:"purpose,omitempty" jsonschema:"only when Rudolfo told you why"`
	RelatedTransactionID string                    `json:"related_transaction_id,omitempty" jsonschema:"existing transaction that is this same charge (from the conversation or a notification)"`
	RefundOfID           string                    `json:"refund_of_id,omitempty" jsonschema:"for refund: the purchase it returns money from"`
	ConfirmDistinct      bool                      `json:"confirm_distinct,omitempty" jsonschema:"true when a possible_match candidate is a different purchase"`
	DeferMatch           bool                      `json:"defer_match,omitempty" jsonschema:"true when you cannot decide on a possible_match now: Lastro records it apart, pending clarification, and opens a possible_match question for Rudolfo"`
	Question             string                    `json:"question,omitempty" jsonschema:"for unknown: what to ask Rudolfo, in Portuguese"`
}

type ReadingDoubtInput struct {
	Page     int    `json:"page" jsonschema:"page of the PDF, starting at 1"`
	Excerpt  string `json:"excerpt" jsonschema:"the text you could not read safely, as printed"`
	Question string `json:"question" jsonschema:"what to ask Rudolfo, in Portuguese"`
}

type SubmitStatementExtractionInput struct {
	Issuer          string               `json:"issuer" jsonschema:"card issuer as printed, such as the bank name"`
	CardLast4       string               `json:"card_last4" jsonschema:"last 4 digits of the holder's card"`
	ClosingDate     string               `json:"closing_date" jsonschema:"closing date printed on the statement, YYYY-MM-DD"`
	DueDate         string               `json:"due_date" jsonschema:"due date printed on the statement, YYYY-MM-DD"`
	IssueDate       string               `json:"issue_date,omitempty" jsonschema:"issue date, when printed"`
	PeriodStart     string               `json:"period_start,omitempty" jsonschema:"first day of the cycle, when printed"`
	PeriodEnd       string               `json:"period_end,omitempty" jsonschema:"last day of the cycle, when printed"`
	Currency        string               `json:"currency,omitempty" jsonschema:"ISO 4217 code of the statement, BRL when omitted"`
	Total           string               `json:"total" jsonschema:"total of this statement as printed, canonical decimal; negative only for a credit balance"`
	PreviousBalance string               `json:"previous_balance,omitempty" jsonschema:"total of the previous statement as printed in the summary, when shown"`
	Reading         string               `json:"reading" jsonschema:"complete when you read every line of every page; partial otherwise"`
	Missing         string               `json:"missing,omitempty" jsonschema:"for a partial reading: what you could not read, in Portuguese"`
	PageCount       int                  `json:"page_count,omitempty" jsonschema:"number of pages of the PDF"`
	FileSHA256      string               `json:"file_sha256,omitempty" jsonschema:"SHA-256 of the PDF in hexadecimal, only if you can compute it"`
	Items           []StatementItemInput `json:"items" jsonschema:"every line of the statement, in the order printed"`
	ReadingDoubts   []ReadingDoubtInput  `json:"reading_doubts,omitempty" jsonschema:"passages you could not interpret safely"`
	DryRun          bool                 `json:"dry_run,omitempty" jsonschema:"true to validate and review the reading without recording anything"`
}

type StatementOutput struct {
	ID              string       `json:"id"`
	Issuer          string       `json:"issuer"`
	CardLast4       string       `json:"card_last4"`
	ClosingDate     string       `json:"closing_date"`
	DueDate         string       `json:"due_date"`
	Month           string       `json:"month" jsonschema:"month this statement belongs to in monthly comparisons, YYYY-MM"`
	MonthBasis      string       `json:"month_basis" jsonschema:"closing or due: which date names the month, as Rudolfo chose"`
	Version         int          `json:"version" jsonschema:"1 for the first reading of this statement"`
	Total           MoneyOutput  `json:"total" jsonschema:"total printed on the statement"`
	PreviousBalance *MoneyOutput `json:"previous_balance,omitempty"`
	Reading         string       `json:"reading"`
	Missing         *string      `json:"missing,omitempty"`
}

type StatementLineOutput struct {
	Index         int         `json:"index" jsonschema:"position of the item in items, starting at 0"`
	Kind          string      `json:"kind"`
	Amount        MoneyOutput `json:"amount"`
	Page          int         `json:"page"`
	Effect        string      `json:"effect" jsonschema:"created (new transaction), linked (confirmed an existing one) or none (kept only in the statement)"`
	TransactionID *string     `json:"transaction_id,omitempty"`
	Description   *string     `json:"description,omitempty"`
	Status        *string     `json:"status,omitempty"`
	Category      *string     `json:"category,omitempty" jsonschema:"category code of the transaction"`
	Changed       []string    `json:"changed,omitempty" jsonschema:"fields of the existing transaction filled or corrected by the statement"`
	Conflicts     []string    `json:"conflicts,omitempty" jsonschema:"fields Rudolfo confirmed that the statement contradicts; a clarification was opened"`
}

type ItemCandidatesOutput struct {
	Index      int                    `json:"index" jsonschema:"position of the item in items, starting at 0"`
	Candidates []MatchCandidateOutput `json:"candidates"`
}

type SubmitStatementExtractionOutput struct {
	Outcome        Outcome                `json:"outcome"`
	Result         string                 `json:"result" jsonschema:"recorded, new_version (a different reading of a statement already recorded), installments_planned (the same reading again gave installment lines recorded before installment plans their purchase and plan), previewed (dry_run: nothing recorded, whatever the reading would do), already_recorded (nothing changed) or possible_match"`
	Statement      *StatementOutput       `json:"statement,omitempty"`
	Review         *StatementReviewOutput `json:"review,omitempty" jsonschema:"composition, computed total and status of the statement"`
	Diff           *StatementDiffOutput   `json:"diff,omitempty" jsonschema:"for a new version: what changed since the previous one"`
	Lines          []StatementLineOutput  `json:"lines,omitempty"`
	Candidates     []ItemCandidatesOutput `json:"candidates,omitempty" jsonschema:"for possible_match: existing transactions that may be the same charge as an item"`
	Clarifications []ClarificationOutput  `json:"clarifications,omitempty" jsonschema:"open questions about this statement and its transactions"`
	Summary        string                 `json:"summary"`
}

type DiffLineOutput struct {
	Index         int         `json:"index" jsonschema:"position in that version's items, starting at 0"`
	Kind          string      `json:"kind"`
	Description   string      `json:"description"`
	Amount        MoneyOutput `json:"amount"`
	Date          *string     `json:"date,omitempty"`
	TransactionID *string     `json:"transaction_id,omitempty"`
}

type DiffChangeOutput struct {
	Before DiffLineOutput `json:"before"`
	After  DiffLineOutput `json:"after"`
	Fields []string       `json:"fields" jsonschema:"amount and/or kind"`
}

type MetadataChangeOutput struct {
	Field  string `json:"field"`
	Before string `json:"before"`
	After  string `json:"after"`
}

type StatementDiffOutput struct {
	FromVersion int                    `json:"from_version"`
	ToVersion   int                    `json:"to_version"`
	Cause       string                 `json:"cause" jsonschema:"reissued (a different PDF), reread (the same PDF read again) or unknown (no file hash on one of the versions)"`
	Added       []DiffLineOutput       `json:"added,omitempty" jsonschema:"lines only in the new version; they were recorded like any line"`
	Removed     []DiffLineOutput       `json:"removed,omitempty" jsonschema:"lines no longer in the statement; their transactions stay and a question asks whether they still hold"`
	Changed     []DiffChangeOutput     `json:"changed,omitempty" jsonschema:"same line with another amount or kind; the same transaction was corrected, never duplicated"`
	Metadata    []MetadataChangeOutput `json:"metadata,omitempty" jsonschema:"total, dates, previous balance or reading that changed"`
}

type CompositionGroupOutput struct {
	Amount MoneyOutput `json:"amount"`
	Lines  int         `json:"lines"`
}

type StatementCompositionOutput struct {
	PreviousBalance *MoneyOutput           `json:"previous_balance,omitempty" jsonschema:"as printed; it explains the charge but is not new spending"`
	Purchases       CompositionGroupOutput `json:"purchases"`
	Installments    CompositionGroupOutput `json:"installments" jsonschema:"installments and Pix installments charged in this cycle"`
	Charges         CompositionGroupOutput `json:"charges" jsonschema:"interest, fees and taxes"`
	Payments        CompositionGroupOutput `json:"payments" jsonschema:"subtracted from the total"`
	Credits         CompositionGroupOutput `json:"credits" jsonschema:"credits and refunds, subtracted from the total"`
	Adjustments     CompositionGroupOutput `json:"adjustments" jsonschema:"net of adjustments: positive adds, negative subtracts"`
	Unknown         CompositionGroupOutput `json:"unknown" jsonschema:"net of lines not interpreted yet"`
	IncludedCharges CompositionGroupOutput `json:"included_charges" jsonschema:"interest, IOF and fees inside installment amounts, as detailed by the statement; already counted in installments and never added again"`
	ComputedTotal   MoneyOutput            `json:"computed_total" jsonschema:"previous balance plus charged lines minus payments and credits, computed by Lastro"`
	InformedTotal   MoneyOutput            `json:"informed_total" jsonschema:"total printed on the statement"`
	Difference      MoneyOutput            `json:"difference" jsonschema:"informed minus computed; zero when the composition explains the printed total"`
}

type ReviewPendingOutput struct {
	Kind        string `json:"kind" jsonschema:"partial_reading, unknown_lines, reading_doubts, difference, unmatched_purchases or clarifications"`
	Description string `json:"description" jsonschema:"in Portuguese, to relay to Rudolfo"`
}

type StatementReviewOutput struct {
	Status      string                     `json:"status" jsonschema:"reconciled (the reading is complete and the composition explains the printed total), divergent or read_incomplete; reconciled never means paid"`
	Composition StatementCompositionOutput `json:"composition"`
	Pending     []ReviewPendingOutput      `json:"pending,omitempty" jsonschema:"what is still missing to check this statement"`
	Unmatched   []TransactionOutput        `json:"unmatched,omitempty" jsonschema:"card purchases recorded from the conversation or Google Wallet in this cycle that no line of this statement confirmed; they stay provisional and may appear in the next statement"`
}

type GetStatementReviewInput struct {
	StatementID string `json:"statement_id,omitempty" jsonschema:"statement to review; omit for the latest closing date"`
	ClosingDate string `json:"closing_date,omitempty" jsonschema:"or the closing date of the statement, YYYY-MM-DD"`
	CardLast4   string `json:"card_last4,omitempty" jsonschema:"holder's card, only when more than one card closes on that date"`
}

type GetStatementReviewOutput struct {
	Statement      StatementOutput       `json:"statement"`
	Review         StatementReviewOutput `json:"review"`
	Clarifications []ClarificationOutput `json:"clarifications,omitempty" jsonschema:"open questions about this statement and its transactions"`
	Summary        string                `json:"summary"`
}

type GetStatementLineSourceInput struct {
	TransactionID string `json:"transaction_id,omitempty" jsonschema:"transaction whose statement lines to locate"`
	StatementID   string `json:"statement_id,omitempty" jsonschema:"or a statement, together with index"`
	Index         *int   `json:"index,omitempty" jsonschema:"position of the line in the reading, starting at 0"`
}

type LineSourceOutput struct {
	StatementID   string      `json:"statement_id"`
	Issuer        string      `json:"issuer"`
	CardLast4     string      `json:"card_last4"`
	ClosingDate   string      `json:"closing_date"`
	Index         int         `json:"index"`
	Kind          string      `json:"kind"`
	Amount        MoneyOutput `json:"amount"`
	Page          int         `json:"page" jsonschema:"page of Rudolfo's PDF where the line is printed"`
	Excerpt       string      `json:"excerpt" jsonschema:"the line as printed, as you read it"`
	TransactionID *string     `json:"transaction_id,omitempty"`
}

type GetStatementLineSourceOutput struct {
	Sources []LineSourceOutput `json:"sources"`
	Summary string             `json:"summary"`
}

func (o SubmitStatementExtractionOutput) reportedOutcome() Outcome {
	return o.Outcome
}

func (s *Server) registerStatements() {
	addTool(s, toolSpec{
		name:  "submit_statement_extraction",
		title: "Submit your reading of a credit card statement",
		description: "Records the statement PDF Rudolfo sent you, as you read it: identity (issuer, card, closing date), dates, the printed total and every line with its page and excerpt. " +
			"Lastro never sees the PDF. The reading is accepted whole or rejected whole; resending the same reading changes nothing. Read get_instructions topic statements first.",
		scope:      accounts.ScopeStatementsWrite,
		idempotent: true,
		enums: map[string][]any{
			"reading":                 enumValues([]ledger.Reading{ledger.ReadingComplete, ledger.ReadingPartial}),
			"items.kind":              enumValues(ledger.StatementItemKinds),
			"items.direction":         enumValues([]ledger.Direction{ledger.DirectionDebit, ledger.DirectionCredit}),
			"items.components.kind":   enumValues(ledger.ComponentKinds),
			"items.original_currency": stringValues(money.SupportedCurrencies()),
			"currency":                stringValues(money.SupportedCurrencies()),
		},
	}, func(ctx context.Context, c call, in SubmitStatementExtractionInput) (SubmitStatementExtractionOutput, error) {
		extraction, err := statementExtraction(in)
		if err != nil {
			return SubmitStatementExtractionOutput{}, err
		}
		extraction.DryRun = in.DryRun
		submission, err := s.deps.Ledger.SubmitStatement(ctx, c.principal.AccountID, extraction, c.receivedAt, c.actor(ledger.AuthorAgent, ""))
		if err != nil {
			return SubmitStatementExtractionOutput{}, err
		}
		return statementSubmissionOutput(submission), nil
	})

	addTool(s, toolSpec{
		name:        "get_statement_review",
		title:       "Review a statement",
		description: "Answers \"what is left to check on this statement?\": the composition (previous balance, purchases, installments, charges, payments and credits), the total Lastro computed from the lines against the printed total, the status and every pending item. Defaults to the latest statement.",
		scope:       accounts.ScopeExpensesRead,
		readOnly:    true,
		idempotent:  true,
	}, func(ctx context.Context, c call, in GetStatementReviewInput) (GetStatementReviewOutput, error) {
		ref, err := statementRef(in)
		if err != nil {
			return GetStatementReviewOutput{}, err
		}
		review, err := s.deps.Ledger.StatementReview(ctx, c.principal.AccountID, ref)
		if errors.Is(err, ledger.ErrStatementNotFound) {
			return GetStatementReviewOutput{}, &ToolError{Outcome: OutcomeFailed, Code: "not_found", Message: err.Error()}
		}
		if err != nil {
			return GetStatementReviewOutput{}, err
		}
		out := GetStatementReviewOutput{Statement: *statementOutput(review.Statement), Review: reviewOutput(review), Summary: reviewSummary(review)}
		for _, clarification := range review.Clarifications {
			out.Clarifications = append(out.Clarifications, clarificationOutput(clarification))
		}
		return out, nil
	})

	addTool(s, toolSpec{
		name:        "get_statement_line_source",
		title:       "Locate a line in the statement PDF",
		description: "Returns the page and the excerpt you sent for a statement line, so Rudolfo can check it in his PDF. Ask by transaction (every statement line that charges it) or by statement and index.",
		scope:       accounts.ScopeExpensesRead,
		readOnly:    true,
		idempotent:  true,
	}, func(ctx context.Context, c call, in GetStatementLineSourceInput) (GetStatementLineSourceOutput, error) {
		sources, err := s.lineSources(ctx, c, in)
		if errors.Is(err, ledger.ErrStatementNotFound) {
			return GetStatementLineSourceOutput{}, &ToolError{Outcome: OutcomeFailed, Code: "not_found", Message: err.Error()}
		}
		if err != nil {
			return GetStatementLineSourceOutput{}, err
		}
		out := GetStatementLineSourceOutput{Sources: []LineSourceOutput{}}
		for _, source := range sources {
			out.Sources = append(out.Sources, lineSourceOutput(source))
		}
		switch len(out.Sources) {
		case 0:
			out.Summary = "Este lançamento não aparece em nenhuma fatura enviada."
		case 1:
			out.Summary = fmt.Sprintf("Na fatura com fechamento em %s, página %d: \"%s\".", sources[0].Statement.ClosingDate.Format(), sources[0].Page, sources[0].Excerpt)
		default:
			out.Summary = fmt.Sprintf("Aparece em %d linhas de fatura.", len(out.Sources))
		}
		return out, nil
	})
}

func statementRef(in GetStatementReviewInput) (ledger.StatementRef, error) {
	var ref ledger.StatementRef
	var err error
	if ref.StatementID, err = optionalID("statement_id", in.StatementID); err != nil {
		return ref, err
	}
	if ref.ClosingDate, err = parseOptionalDate("closing_date", in.ClosingDate); err != nil {
		return ref, err
	}
	ref.CardLast4 = optionalString(in.CardLast4)
	return ref, nil
}

func (s *Server) lineSources(ctx context.Context, c call, in GetStatementLineSourceInput) ([]ledger.LineSource, error) {
	transactionID, err := optionalID("transaction_id", in.TransactionID)
	if err != nil {
		return nil, err
	}
	if transactionID != nil {
		return s.deps.Ledger.TransactionLineSources(ctx, c.principal.AccountID, *transactionID)
	}
	statementID, err := optionalID("statement_id", in.StatementID)
	if err != nil {
		return nil, err
	}
	if statementID == nil || in.Index == nil {
		return nil, invalid("transaction_id", "send transaction_id, or statement_id with index")
	}
	source, err := s.deps.Ledger.StatementLineSource(ctx, c.principal.AccountID, *statementID, *in.Index+1)
	if err != nil {
		return nil, err
	}
	return []ledger.LineSource{source}, nil
}

func lineSourceOutput(source ledger.LineSource) LineSourceOutput {
	out := LineSourceOutput{
		StatementID: source.Statement.ID.String(),
		Issuer:      source.Statement.Issuer,
		CardLast4:   source.Statement.CardLast4,
		ClosingDate: source.Statement.ClosingDate.String(),
		Index:       source.Position - 1,
		Kind:        string(source.Kind),
		Amount:      moneyOutput(source.Amount),
		Page:        source.Page,
		Excerpt:     source.Excerpt,
	}
	if source.TransactionID != nil {
		id := source.TransactionID.String()
		out.TransactionID = &id
	}
	return out
}

func reviewOutput(review ledger.StatementReview) StatementReviewOutput {
	composition := review.Composition
	group := func(g ledger.CompositionGroup) CompositionGroupOutput {
		return CompositionGroupOutput{Amount: moneyOutput(g.Amount), Lines: g.Lines}
	}
	out := StatementReviewOutput{
		Status: string(review.Status),
		Composition: StatementCompositionOutput{
			Purchases:       group(composition.Purchases),
			Installments:    group(composition.Installments),
			Charges:         group(composition.Charges),
			Payments:        group(composition.Payments),
			Credits:         group(composition.Credits),
			Adjustments:     group(composition.Adjustments),
			Unknown:         group(composition.Unknown),
			IncludedCharges: group(composition.IncludedCharges),
			ComputedTotal:   moneyOutput(composition.ComputedTotal),
			InformedTotal:   moneyOutput(composition.InformedTotal),
			Difference:      moneyOutput(composition.Difference),
		},
	}
	if composition.PreviousBalance != nil {
		previous := moneyOutput(*composition.PreviousBalance)
		out.Composition.PreviousBalance = &previous
	}
	for _, pending := range review.Pending {
		out.Pending = append(out.Pending, ReviewPendingOutput{Kind: string(pending.Kind), Description: pending.Description})
	}
	for _, transaction := range review.Unmatched {
		out.Unmatched = append(out.Unmatched, transactionOutput(transaction, nil))
	}
	return out
}

func reviewSummary(review ledger.StatementReview) string {
	statement, composition := review.Statement, review.Composition
	head := fmt.Sprintf("Fatura %s final %s, fechamento %s: ", statement.Issuer, statement.CardLast4, statement.ClosingDate.Format())
	switch review.Status {
	case ledger.ReviewReconciled:
		head += fmt.Sprintf("conferida. O total calculado a partir dos itens (%s) explica o total da fatura. Conferida não quer dizer paga.", composition.ComputedTotal.Format())
	case ledger.ReviewDivergent:
		head += fmt.Sprintf("não confere. Total da fatura %s, total calculado %s, diferença de %s.", composition.InformedTotal.Format(), composition.ComputedTotal.Format(), composition.Difference.Format())
	default:
		head += fmt.Sprintf("leitura incompleta, ainda não pode ser dada como conferida. Total da fatura %s, total calculado %s.", composition.InformedTotal.Format(), composition.ComputedTotal.Format())
	}
	if len(review.Pending) > 0 {
		head += fmt.Sprintf(" %d pendência(s).", len(review.Pending))
	}
	return head
}

func stringValues(values []string) []any {
	out := make([]any, len(values))
	for i, v := range values {
		out[i] = v
	}
	return out
}

func statementExtraction(in SubmitStatementExtractionInput) (ledger.StatementExtraction, error) {
	currency := strings.TrimSpace(in.Currency)
	if currency == "" {
		currency = defaultCurrency
	}
	out := ledger.StatementExtraction{
		Issuer:    in.Issuer,
		CardLast4: in.CardLast4,
		Reading:   ledger.Reading(strings.TrimSpace(in.Reading)),
		Missing:   in.Missing,
	}
	var err error
	if out.ClosingDate, err = requiredDate("closing_date", in.ClosingDate); err != nil {
		return out, err
	}
	if out.DueDate, err = requiredDate("due_date", in.DueDate); err != nil {
		return out, err
	}
	if out.IssueDate, err = parseOptionalDate("issue_date", in.IssueDate); err != nil {
		return out, err
	}
	if out.PeriodStart, err = parseOptionalDate("period_start", in.PeriodStart); err != nil {
		return out, err
	}
	if out.PeriodEnd, err = parseOptionalDate("period_end", in.PeriodEnd); err != nil {
		return out, err
	}
	if out.Total, err = parseMoney("total", in.Total, currency); err != nil {
		return out, err
	}
	if strings.TrimSpace(in.PreviousBalance) != "" {
		balance, err := parseMoney("previous_balance", in.PreviousBalance, currency)
		if err != nil {
			return out, err
		}
		out.PreviousBalance = &balance
	}
	if in.PageCount != 0 {
		out.PageCount = &in.PageCount
	}
	if hash := strings.TrimSpace(in.FileSHA256); hash != "" {
		if out.FileSHA256, err = hex.DecodeString(strings.ToLower(hash)); err != nil {
			return out, invalid("file_sha256", "must be the SHA-256 of the PDF in hexadecimal")
		}
	}
	out.Items = make([]ledger.StatementItem, len(in.Items))
	for i, item := range in.Items {
		if out.Items[i], err = statementItem(item, currency); err != nil {
			var toolErr *ToolError
			if errors.As(err, &toolErr) {
				toolErr.Field = fmt.Sprintf("items[%d].%s", i, toolErr.Field)
			}
			return out, err
		}
	}
	out.Doubts = make([]ledger.ReadingDoubt, len(in.ReadingDoubts))
	for i, doubt := range in.ReadingDoubts {
		out.Doubts[i] = ledger.ReadingDoubt{Page: doubt.Page, Excerpt: doubt.Excerpt, Question: doubt.Question}
	}
	return out, nil
}

func statementItem(in StatementItemInput, currency string) (ledger.StatementItem, error) {
	item := ledger.StatementItem{
		Kind:                ledger.StatementItemKind(strings.TrimSpace(in.Kind)),
		Direction:           ledger.Direction(strings.TrimSpace(in.Direction)),
		OriginalDescription: in.OriginalDescription,
		Description:         in.Description,
		Merchant:            optionalString(in.Merchant),
		CardLast4:           optionalString(in.CardLast4),
		Page:                in.Page,
		Excerpt:             in.Excerpt,
		Category:            optionalString(in.Category),
		Purpose:             optionalString(in.Purpose),
		ConfirmDistinct:     in.ConfirmDistinct,
		DeferMatch:          in.DeferMatch,
		Question:            in.Question,
	}
	var err error
	if item.Amount, err = parseMoney("amount", in.Amount, currency); err != nil {
		return item, err
	}
	switch original, code := strings.TrimSpace(in.OriginalAmount), strings.TrimSpace(in.OriginalCurrency); {
	case original != "" && code != "":
		amount, err := parseMoney("original_amount", original, code)
		if err != nil {
			return item, err
		}
		item.OriginalAmount = &amount
	case original != "" || code != "":
		return item, invalid("original_amount", "and original_currency go together")
	}
	if item.Date, err = parseOptionalDate("date", in.Date); err != nil {
		return item, err
	}
	if in.InstallmentNumber != 0 || in.InstallmentCount != 0 {
		item.Installment = &ledger.InstallmentPosition{Number: in.InstallmentNumber, Count: in.InstallmentCount}
	}
	for i, component := range in.Components {
		amount, err := parseMoney(fmt.Sprintf("components[%d].amount", i), component.Amount, currency)
		if err != nil {
			return item, err
		}
		item.Components = append(item.Components, ledger.ChargeComponent{Kind: ledger.ComponentKind(strings.TrimSpace(component.Kind)), Amount: amount, Included: component.Included})
	}
	for i, future := range in.FutureInstallments {
		amount, err := parseMoney(fmt.Sprintf("future_installments[%d].amount", i), future.Amount, currency)
		if err != nil {
			return item, err
		}
		item.FutureInstallments = append(item.FutureInstallments, ledger.FutureInstallment{Number: future.Number, Amount: amount})
	}
	if item.RelatedTransactionID, err = optionalID("related_transaction_id", in.RelatedTransactionID); err != nil {
		return item, err
	}
	if item.RefundOfID, err = optionalID("refund_of_id", in.RefundOfID); err != nil {
		return item, err
	}
	return item, nil
}

func requiredDate(field, value string) (calendar.Date, error) {
	date, err := parseOptionalDate(field, value)
	if err != nil {
		return calendar.Date{}, err
	}
	if date == nil {
		return calendar.Date{}, invalid(field, "is required; read it from the statement")
	}
	return *date, nil
}

func parseMoney(field, value, currency string) (money.Money, error) {
	amount, err := money.Parse(strings.TrimSpace(value), currency)
	switch {
	case errors.Is(err, money.ErrUnsupportedCurrency):
		return money.Money{}, invalid(strings.Replace(field, "amount", "currency", 1), err.Error())
	case err != nil:
		return money.Money{}, invalid(field, err.Error())
	}
	return amount, nil
}

func optionalID(field, value string) (*uuid.UUID, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	id, err := parseID(field, value)
	if err != nil {
		return nil, err
	}
	return &id, nil
}

func statementSubmissionOutput(submission ledger.StatementSubmission) SubmitStatementExtractionOutput {
	out := SubmitStatementExtractionOutput{Outcome: OutcomeCompleted, Result: string(submission.Result)}
	if submission.Statement != nil {
		out.Statement = statementOutput(*submission.Statement)
	}
	for _, line := range submission.Lines {
		out.Lines = append(out.Lines, statementLineOutput(line))
	}
	for _, item := range submission.Candidates {
		out.Candidates = append(out.Candidates, ItemCandidatesOutput{Index: item.Position - 1, Candidates: candidateOutputs(item.Candidates)})
	}
	if submission.Diff != nil {
		out.Diff = diffOutput(*submission.Diff)
	}
	if review := submission.Review; review != nil {
		reviewed := reviewOutput(*review)
		out.Review = &reviewed
		for _, clarification := range review.Clarifications {
			out.Clarifications = append(out.Clarifications, clarificationOutput(clarification))
		}
	}
	if submission.Result == ledger.StatementPossibleMatch {
		out.Outcome = OutcomePending
	}
	out.Summary = statementSummary(submission)
	return out
}

func diffOutput(diff ledger.StatementDiff) *StatementDiffOutput {
	line := func(l ledger.DiffLine) DiffLineOutput {
		out := DiffLineOutput{Index: l.Position - 1, Kind: string(l.Kind), Description: l.Description, Amount: moneyOutput(l.Amount)}
		if l.Date != nil {
			date := l.Date.String()
			out.Date = &date
		}
		if l.TransactionID != nil {
			id := l.TransactionID.String()
			out.TransactionID = &id
		}
		return out
	}
	out := &StatementDiffOutput{FromVersion: diff.FromVersion, ToVersion: diff.ToVersion, Cause: string(diff.Cause)}
	for _, l := range diff.Added {
		out.Added = append(out.Added, line(l))
	}
	for _, l := range diff.Removed {
		out.Removed = append(out.Removed, line(l))
	}
	for _, change := range diff.Changed {
		out.Changed = append(out.Changed, DiffChangeOutput{Before: line(change.Before), After: line(change.After), Fields: change.Fields})
	}
	for _, change := range diff.Metadata {
		out.Metadata = append(out.Metadata, MetadataChangeOutput{Field: change.Field, Before: change.Before, After: change.After})
	}
	return out
}

func statementOutput(statement ledger.Statement) *StatementOutput {
	out := &StatementOutput{
		ID:          statement.ID.String(),
		Issuer:      statement.Issuer,
		CardLast4:   statement.CardLast4,
		ClosingDate: statement.ClosingDate.String(),
		DueDate:     statement.Version.DueDate.String(),
		Month:       statement.Month,
		MonthBasis:  string(statement.MonthBasis),
		Version:     statement.Version.Number,
		Total:       moneyOutput(statement.Version.Total),
		Reading:     string(statement.Version.Reading),
		Missing:     statement.Version.Missing,
	}
	if balance := statement.Version.PreviousBalance; balance != nil {
		previous := moneyOutput(*balance)
		out.PreviousBalance = &previous
	}
	return out
}

func statementLineOutput(line ledger.StatementLine) StatementLineOutput {
	out := StatementLineOutput{
		Index:  line.Position - 1,
		Kind:   string(line.Kind),
		Amount: moneyOutput(line.Amount),
		Page:   line.Page,
		Effect: string(line.Effect),
	}
	if tr := line.Transaction; tr != nil {
		id, status := tr.ID.String(), string(tr.Status)
		out.TransactionID, out.Description, out.Status = &id, &tr.Description, &status
		if line.Category.Status != ledger.AllocationMissing {
			out.Category = &line.Category.Code
		}
	}
	for _, field := range line.Changed {
		out.Changed = append(out.Changed, string(field))
	}
	for _, conflict := range line.Conflicts {
		out.Conflicts = append(out.Conflicts, string(conflict.Field))
	}
	return out
}

func statementSummary(submission ledger.StatementSubmission) string {
	switch submission.Result {
	case ledger.StatementPossibleMatch:
		return fmt.Sprintf("Nada foi registrado: %d item(ns) podem ser cobranças já registradas pela conversa ou por notificação. Para cada um, envie de novo a leitura inteira com related_transaction_id (mesma compra) ou confirm_distinct (outra compra); pergunte a Rudolfo se não tiver certeza.", len(submission.Candidates))
	case ledger.StatementAlreadyRecorded:
		return "Esta leitura da fatura já estava registrada; nada mudou. " + reviewSummary(*submission.Review)
	case ledger.StatementPlanned:
		return fmt.Sprintf("Esta leitura da fatura já estava registrada. %d linha(s) de parcela ganharam a compra e o plano de parcelamento; nenhum outro lançamento mudou. %s", submission.Planned, reviewSummary(*submission.Review))
	case ledger.StatementNewVersion:
		diff := submission.Diff
		return fmt.Sprintf("A fatura já estava registrada e esta leitura é diferente: virou a versão %d. %d linha(s) nova(s), %d removida(s) e %d alterada(s); nenhum lançamento foi duplicado e as diferenças viraram dúvidas para Rudolfo conferir. %s",
			diff.ToVersion, len(diff.Added), len(diff.Removed), len(diff.Changed), reviewSummary(*submission.Review))
	}
	statement := submission.Statement
	counts := map[ledger.InterpretationEffect]int{}
	for _, line := range submission.Lines {
		counts[line.Effect]++
	}
	if submission.Result == ledger.StatementPreviewed && submission.Planned > 0 {
		return fmt.Sprintf("Simulação, nada foi gravado. Esta leitura já está registrada; enviada sem dry_run, %d linha(s) de parcela ganhariam a compra e o plano de parcelamento. %s", submission.Planned, reviewSummary(*submission.Review))
	}
	verb := "Registrei"
	if submission.Result == ledger.StatementPreviewed {
		verb = "Simulação, nada foi gravado. Eu registraria"
	}
	summary := fmt.Sprintf("%s a fatura %s final %s (fechamento %s, vencimento %s, total %s): %d itens, %d lançamento(s) novo(s), %d conferido(s) com registros existentes e %d só na fatura.", verb,
		statement.Issuer, statement.CardLast4, statement.ClosingDate.Format(), statement.Version.DueDate.Format(), statement.Version.Total.Format(),
		len(submission.Lines), counts[ledger.EffectCreated], counts[ledger.EffectLinked], counts[ledger.EffectNone])
	return summary + " " + reviewSummary(*submission.Review)
}
