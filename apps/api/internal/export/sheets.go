package export

import (
	"context"
	"fmt"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/rudolfoborges/lastro/apps/api/internal/money"
)

type sheet struct {
	name    string
	query   string
	columns []string
	amounts map[int]int
	rows    [][]string
}

// Each sheet query returns text columns. Amounts come in minor units and are
// written as decimals; amounts maps each amount column to its currency column.
var sheetQueries = []sheet{
	{
		name: "transactions.csv",
		columns: []string{"id", "date", "description", "merchant", "payee", "amount", "currency", "original_amount", "original_currency", "amount_basis",
			"nature", "status", "payment_method", "card_last4", "category", "category_status", "purpose", "refund_of_id", "merged_into_id",
			"installment_count", "installment_amount", "created_at"},
		amounts: map[int]int{5: 6, 7: 8, 20: 6},
		query: `SELECT t.id::text, coalesce(t.occurred_on::text, ''), t.description, coalesce(t.merchant, ''), coalesce(t.payee, ''),
		               t.amount_minor::text, t.currency,
		               coalesce(t.original_amount_minor::text, ''), coalesce(t.original_currency, ''), t.amount_basis,
		               t.nature, t.status, coalesce(pm.kind, ''), coalesce(pm.card_last4, ''), coalesce(c.code, ''), coalesce(a.status, ''),
		               coalesce(t.purpose, ''), coalesce(t.refund_of_id::text, ''), coalesce(t.merged_into_id::text, ''),
		               coalesce(t.installment_count::text, ''), coalesce(t.installment_amount_minor::text, ''),
		               to_char(t.created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
		        FROM transactions t
		        LEFT JOIN payment_methods pm ON pm.account_id = t.account_id AND pm.id = t.payment_method_id
		        LEFT JOIN category_allocations a ON a.account_id = t.account_id AND a.transaction_id = t.id AND a.superseded_at IS NULL
		        LEFT JOIN categories c ON c.account_id = a.account_id AND c.id = a.category_id
		        WHERE t.account_id = $1
		        ORDER BY t.occurred_on NULLS LAST, t.created_at, t.id`,
	},
	{
		name:    "allocations.csv",
		columns: []string{"transaction_id", "category", "amount", "currency", "status", "origin", "created_at", "superseded_at"},
		amounts: map[int]int{2: 3},
		query: `SELECT a.transaction_id::text, c.code, a.amount_minor::text, t.currency, a.status, a.origin,
		               to_char(a.created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
		               coalesce(to_char(a.superseded_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'), '')
		        FROM category_allocations a
		        JOIN categories c ON c.account_id = a.account_id AND c.id = a.category_id
		        JOIN transactions t ON t.account_id = a.account_id AND t.id = a.transaction_id
		        WHERE a.account_id = $1
		        ORDER BY a.created_at, a.id`,
	},
	{
		name:    "installments.csv",
		columns: []string{"transaction_id", "modality", "installment_count", "installment_amount", "installment_amount_known", "total", "currency", "number", "state", "amount", "closing_date", "informed_on"},
		amounts: map[int]int{3: 6, 5: 6, 9: 6},
		query: `SELECT p.transaction_id::text, p.payment_modality, p.installment_count::text,
		               coalesce(p.installment_amount_minor::text, ''), p.installment_amount_known::text,
		               coalesce(p.total_minor::text, ''), p.currency,
		               coalesce(i.number::text, ''), coalesce(i.state, ''), coalesce(i.amount_minor::text, ''),
		               coalesce(s.closing_date::text, ''), coalesce(i.informed_on::text, p.informed_on::text)
		        FROM installment_plans p
		        LEFT JOIN installments i ON i.account_id = p.account_id AND i.plan_id = p.id
		        LEFT JOIN statements s ON s.account_id = i.account_id AND s.id = i.statement_id
		        WHERE p.account_id = $1 AND p.canceled_at IS NULL
		        ORDER BY p.created_at, p.id, i.number`,
	},
	{
		name:    "statements.csv",
		columns: []string{"statement_id", "issuer", "card_last4", "closing_date", "due_date", "version", "total", "previous_balance", "currency", "reading"},
		amounts: map[int]int{6: 8, 7: 8},
		query: `SELECT s.id::text, s.issuer, s.card_last4, s.closing_date::text, v.due_date::text, v.version::text,
		               v.informed_total_minor::text, coalesce(v.previous_balance_minor::text, ''),
		               v.currency, v.reading
		        FROM statements s
		        JOIN statement_versions v ON v.account_id = s.account_id AND v.statement_id = s.id
		        WHERE s.account_id = $1
		        ORDER BY s.closing_date, s.card_last4, v.version`,
	},
	{
		name:    "statement_lines.csv",
		columns: []string{"statement_id", "version", "position", "kind", "direction", "amount", "currency", "page", "excerpt", "transaction_id"},
		amounts: map[int]int{5: 6},
		query: `SELECT v.statement_id::text, v.version::text, l.position::text, l.kind, l.direction, l.amount_minor::text,
		               v.currency, l.page::text, l.excerpt, coalesce(e.transaction_id::text, '')
		        FROM statement_lines l
		        JOIN statement_versions v ON v.account_id = l.account_id AND v.id = l.statement_version_id
		        LEFT JOIN transaction_evidence e ON e.account_id = l.account_id AND e.observation_id = l.observation_id AND e.status = 'confirmed'
		        WHERE l.account_id = $1
		        ORDER BY v.statement_id, v.version, l.position`,
	},
}

func (s *Service) sheets(ctx context.Context, accountID uuid.UUID) ([]sheet, error) {
	out := make([]sheet, len(sheetQueries))
	err := s.store.TenantSQL(ctx, accountID, func(tx pgx.Tx) error {
		for i, definition := range sheetQueries {
			rows, err := tx.Query(ctx, definition.query, accountID)
			if err != nil {
				return fmt.Errorf("export %s: %w", definition.name, err)
			}
			values, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) ([]string, error) {
				raw, err := row.Values()
				if err != nil {
					return nil, err
				}
				record := make([]string, len(raw))
				for j, value := range raw {
					record[j], _ = value.(string)
				}
				for amount, currency := range definition.amounts {
					record[amount] = decimal(record[amount], record[currency])
				}
				return record, nil
			})
			if err != nil {
				return fmt.Errorf("export %s: %w", definition.name, err)
			}
			out[i] = sheet{name: definition.name, rows: append([][]string{definition.columns}, values...)}
		}
		return nil
	})
	return out, err
}

func decimal(minor, currency string) string {
	if minor == "" {
		return ""
	}
	value, err := strconv.ParseInt(minor, 10, 64)
	if err != nil {
		return minor
	}
	amount, err := money.New(value, currency)
	if err != nil {
		return minor
	}
	return amount.Decimal()
}
