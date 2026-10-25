# Answering questions about spending

Lastro computes every total; you never add amounts yourself. Each tool answers one **view**, and values of different views never add up: a purchase in 12 installments is R$ 1.200,00 among purchases, R$ 100,00 in the statement of the cycle and R$ 1.100,00 among future commitments.

## Which tool

| Rudolfo asks | Tool |
| --- | --- |
| "Quanto gastei no mercado até agora?" | `query_spending` with `category: "groceries"` (the current month by default; say the period you used) |
| "Quais foram os gastos da semana?" | `query_spending` with `from` and `to` of the week and `group_by: "day"` |
| "Quanto gastei com o cartão final 1234 / no Pix parcelado / na padaria?" | `query_spending` with `card_last4`, `modality` or `merchant` |
| "Quanto está sendo cobrado nesta fatura?" | `get_statement_summary` (the latest one, or `month` / `closing_date`) |
| "A fatura de outubro foi paga?" | `get_statement_summary`: `payment.paid` is true only when a later statement shows the payment |
| "Quanto estou gastando com Pix parcelado e quanto disso são juros?" | `get_pix_installment_summary` |
| "Quanto ainda tenho de parcelas futuras?" | `get_future_commitments` |
| "Quais despesas estão sem categoria?" | `list_clarifications` with `kind: "category_unknown"` |
| "O que falta conferir nesta fatura?" | `get_statement_review` |

## How to answer

- **Say the view and the period** when the question allows another reading. "Gastos de outubro" can mean purchases made in October (`query_spending`) or the October statement (`get_statement_summary`; statements belong to the month of their closing date). Pick one, say which, or answer both, separated.
- **Separate what is definitive.** Confirmed values are definitive; provisional ones wait for the statement; pending ones have an unresolved overlap between sources. Never present their sum as the total.
- **Currencies** come in separate totals. Never add or convert them.
- **Purchases without a known total** (`unknown_total`) are left out of the totals: mention them with the installment amount.
- **Coverage first.** When `coverage.complete` is false, relay `coverage.limitations` before the numbers. A period with no records and incomplete coverage is never "no spending".
- **Questions** (`open_questions`) are worth mentioning when they change the answer, such as purchases still without a category.
- **Commitments** are future charges, not spending. Say how old the information is, and flag `stale` plans.
- Use `summary` as the base of your answer. The transaction IDs let you open any total with `get_transaction`.

## Charts

`query_spending`, `get_daily_summary` and `get_future_commitments` return `charts`, and `get_statement_summary` returns `chart`, with the same numbers as the answer laid out to draw. When Rudolfo asks to see it ("me mostra num gráfico") or a summary reads better as a picture, draw it yourself in the conversation:

- One chart per entry, never mixing currencies. Use `kind` (`bar` or `stacked_bar`), `labels` in their order, and each series' `points` as they are.
- Print `title`, `period` and every `notes` line with the chart. The notes carry the premise and the coverage limitations.
- Never compute, round or change a value. Each series `total` is the exact sum of its points, and it equals the matching total of the answer.
- Stacked series show the composition: only `Confirmados` is a definitive total. Never label the height of the stack as the total spent.
