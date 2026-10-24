# Processing a credit card statement

Rudolfo sends you the monthly statement PDF. **The PDF never goes to Lastro**: you read it and send only your structured reading with `submit_statement_extraction`. Lastro validates it, keeps every line with its page and excerpt, and records it whole or not at all.

## Before reading

- If the PDF is password protected, ask Rudolfo to send it without the password. Never ask for the password and never send it to Lastro.
- Read **every page**. Dates come from the content, never from the file name or from when Rudolfo sent it.
- If you can compute the SHA-256 of the file (for example with a code tool), send it in `file_sha256`. Otherwise leave it out.

## Identity and summary

| Field | What to send |
| --- | --- |
| `issuer` | The issuer as printed (bank name) |
| `card_last4` | Last 4 digits of the **holder's** card |
| `closing_date`, `due_date` | As printed, `YYYY-MM-DD` |
| `issue_date`, `period_start`, `period_end` | Only when printed |
| `total` | Total of this statement as printed ("Total desta fatura") |
| `previous_balance` | Total of the previous statement, when the summary shows it |
| `page_count` | Number of pages of the PDF |

The same issuer, card and closing date identify the statement. Resending the same reading changes nothing. A different reading of a statement already recorded (a new PDF issued by the bank, or your new reading of the same one) becomes a **new version** of it, never a second statement: send the whole reading again, every line, not only what changed.

## Lines

Send **every line** of the statement in `items`, in the order printed, including payments and credits. For each one:

- `kind`:

  | `kind` | When |
  | --- | --- |
  | `purchase` | A purchase charged in full in this cycle |
  | `installment` | An installment of a card purchase ("03/10") |
  | `pix_installment` | An installment of a Pix parcelado |
  | `interest` | Interest (juros, rotativo, atraso) |
  | `fee` | A fee (anuidade, tarifa, multa) |
  | `tax` | A tax (IOF, including "IOF compra internacional") |
  | `credit` | A credit that is not a refund (cashback, bonus, ajuste a favor) |
  | `refund` | Money returned for a purchase (estorno) |
  | `payment` | A payment of the card (pagamento efetuado) |
  | `adjustment` | Another adjustment printed as a line; send `direction` |
  | `unknown` | You cannot tell what the line is; send `direction` and a `question` in Portuguese |

- `original_description` exactly as printed, and `description` only when a readable one helps.
- `amount` as a **positive** canonical decimal in the statement currency (`"32.97"`). The kind says whether it adds or subtracts. `direction` (`debit` adds, `credit` subtracts) goes only with `adjustment` and `unknown`.
- `date` as printed on the line, with the year taken from the cycle. Leave it out when the line has no date.
- `page` and `excerpt` for **every** item: the page of the PDF (starting at 1) and the text of the line exactly as printed. Rudolfo uses them to find the line in his PDF; a line without them is rejected.
- `card_last4` when the line belongs to an additional or virtual card.
- International lines: `amount` is what was charged in reais; send `original_amount` and `original_currency` as printed. The IOF of the purchase is its own `tax` line.
- `installment_number` and `installment_count` for every "n/N" line.
- `components` only when the statement details principal, interest, IOF or fee of a line. `included: true` when the value is inside the line amount (the included ones must add up to the amount, principal too); `included: false` when it is charged in another line, which you also send as its own item. Never invent a breakdown. Lastro never adds a component on top of the line: interest inside a R$ 500,00 installment is part of the R$ 500,00.
- `category`: a suggestion, as in the classification policy. Use `financial_charges` for interest, fees and taxes. Payments and credits need no category.
- `purpose` only when Rudolfo told you.

Leave out what is not a charge: the "próximas faturas" or future installments table, interest and CET simulations, minimum payment and financing offers. Previous balance goes in `previous_balance`, not in `items`. When the future installments table lists the next installments **of a specific purchase**, send them in that purchase's line as `future_installments` (`number` and `amount`); never project them yourself.

## Installments

Every "n/N" line (`installment`, `pix_installment`) is one installment of a purchase. It counts in the statement with its own amount, and it belongs to that purchase's installment plan; the full purchase is never added to the statement again.

- **Chained by Lastro:** when the card, the printed description, N and the amount match exactly one plan whose installment n−1 was charged in an earlier statement, Lastro links the line to that purchase by itself (`linked`).
- **Your decision:** otherwise, when there are plans or purchases that may be the same one, such as a TV Rudolfo told you he bought "em 12x de 100", Lastro answers `possible_match`. Use `related_transaction_id` for the same purchase, `confirm_distinct` for another, or `defer_match` when only Rudolfo can tell.
- **A purchase seen only on the statement:** Lastro creates it from the line (`created`). The purchase date is the line date, and `amount_basis` is `installment`: only the installment amount is known, and the total is **never** multiplied out. Its plan estimates the next installments from the one charged. When Rudolfo tells the total, correct the amount with `update_transaction`; it becomes the total of the plan.
- **Statements recorded before installment plans existed** kept their installment lines without a purchase. Sending the same reading again gives those lines their purchase and plan (`installments_planned`).

**Pix parcelado** (`pix_installment`) is a payment made by Pix and financed on the card; its modality is kept apart from the category and the purpose. Send `purpose` when Rudolfo told you; otherwise Lastro asks him what it was for (goods or services, a transfer or a refinancing). If he says it was a transfer or a refinancing, correct the nature with `update_transaction`. When the statement does not detail principal and interest of a Pix installment, Lastro keeps the amount whole and asks once per plan.

`get_transaction` shows the plan of a purchase: installments charged, still to come (`known_future` or `estimated`), and `remaining`, with the date of the information.

## Doubts and partial readings

- A passage you could not read or interpret safely goes in `reading_doubts`, with `page`, `excerpt` and a `question` in Portuguese.
- If you could not read everything (an unreadable page, a cut table), send `reading: "partial"` and say in `missing` what is missing. Never send `complete` when a page was skipped.

## Check before recording

Send the reading first with `dry_run: true`. Lastro validates it, matches it against what is already recorded and returns the review (composition, computed total and status) without recording anything (`result: "previewed"`), whatever the reading would do. If the computed total does not match the printed total, look for a missing or misread line before recording: a difference usually means your reading is wrong, not the bank. Then send the same reading without `dry_run`.

## Purchases already recorded

Rudolfo's credit card purchases told in the conversation or seen in Google Wallet notifications are waiting for the statement as provisional transactions. Send the reading without links first. When a `purchase` or `refund` line may be one of them, Lastro answers `possible_match`: nothing is recorded, and `candidates` lists, for each item `index`, the transactions with their `reasons` and `differences` (see `get_instructions` topic `matching`). Then send the **whole reading again**, with each listed item decided:

- same purchase: `related_transaction_id`. Lastro links the line, confirms the transaction, fills the card and the missing fields, and keeps what Rudolfo confirmed;
- a different purchase: `confirm_distinct: true`;
- not sure: ask Rudolfo. Two purchases of the same amount on the same day can both be real. When you cannot ask now, send `defer_match: true` on the item: the reading is recorded, the line gets its own transaction pending clarification and a `possible_match` question waits for Rudolfo.

You may also link any line (installments and charges too) to a transaction you already know is the same, with `related_transaction_id`.

## Reading the answer

- `recorded`: done. Each line says its `effect`: `created` (a new transaction, including a purchase seen only through its installment), `linked` (an existing one, now confirmed, or the next installment of a plan) or `none` (kept only in the statement: unknown lines).
- `previewed`: the dry run passed; nothing was recorded.
- `already_recorded`: you had sent the same reading before; nothing changed.
- `installments_planned`: you had sent the same reading before, and its installment lines recorded before installment plans existed got their purchase and plan (`planned` lines have effect `created` or `linked`); nothing else changed.
- `new_version`: the statement was already recorded and this reading differs. `diff` lists the lines added, removed and changed and the summary fields that changed. Nothing is duplicated:
  - a changed line corrects the same transaction (except what Rudolfo confirmed, which becomes a question);
  - a removed line keeps its transaction and opens a question;
  - one more question summarizes the version.

  Tell Rudolfo what changed.
- `possible_match` (`outcome: "pending"`): nothing was recorded; see above.
- `outcome: "failed"`: nothing was recorded. `field` points to the problem, such as `items[3].page`; fix it and send the whole reading again.

Every answer carries `review`:

- `status`:
  - `reconciled` when the reading is complete and the total Lastro computed from the lines (previous balance, plus charges, minus payments and credits) explains the printed total;
  - `divergent` when there is a difference, shown in cents and never hidden in an adjustment;
  - `read_incomplete` while the reading is partial or has unknown lines or reading doubts.
- `reconciled` never means **paid**: only a payment line in a later statement shows that.
- `pending` lists what is still missing, ready to relay.

Tell Rudolfo, using `summary`, what was recorded and ask the open questions in `clarifications`.

## Later

- "O que falta conferir nesta fatura?": `get_statement_review` (the latest statement by default, or `closing_date`), then relay `pending` and the open questions. `list_clarifications` with `statement_id` lists only the questions about that statement.
- "Onde está essa compra na fatura?": `get_statement_line_source` with the `transaction_id` (or `statement_id` and `index`) returns the page and the excerpt you sent, so Rudolfo finds the line in his PDF.
- The review lists in `unmatched` the card purchases of the cycle from the conversation or Google Wallet that no line confirmed. They stay provisional and usually appear in the next statement; never void them for that.

## Never

- Never send the PDF, its content in base64 or the password to Lastro.
- Never record statement lines with `record_expense`: the statement would count them twice.
- Never add up amounts yourself; Lastro computes the totals.
