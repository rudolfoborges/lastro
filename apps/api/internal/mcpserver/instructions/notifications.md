# Processing Google Wallet notifications

Rudolfo's Android phone sends every Google Wallet notification to Lastro, which stores it redacted and **never reads it**. You read each one, decide what happened and send your interpretation back. Until you do, the notification is pending and its purchase is not in any total.

## When to process

Lastro does not call you. Process the inbox:

- when a scheduled task Rudolfo created asks you to;
- at the start of every conversation with Rudolfo, before anything else, if `list_pending_notifications` returns `pending` above zero;
- whenever Rudolfo asks about recent spending.

The coverage status shows Rudolfo how long the inbox has been waiting, so a queue that nobody processes does not go unnoticed.

## Procedure

1. Call `list_pending_notifications`. Follow `next_cursor` until it is absent.
2. For each notification, read `title`, `text` and `big_text`, and decide the `event_type`:

   | `event_type` | When | Effect |
   | --- | --- | --- |
   | `purchase` | A new payment was approved ("Compra de R$ 32,97 em Padaria Pão Quente") | A transaction, or the confirmation of one Rudolfo already told you about |
   | `purchase_update` | A later version of the **same** notification changes a purchase already interpreted (amount, merchant, pending → approved) | Corrects that same transaction; never creates another |
   | `cancellation` | The purchase was cancelled before being charged | Voids the transaction |
   | `refund` | Money was returned ("Estorno de R$ 30,00") | A separate refund transaction; the purchase is kept |
   | `declined` | The payment was refused | Nothing is recorded as an expense |
   | `notice` | Security alerts, card added, limits, promotions | Nothing is recorded |
   | `unrelated` | Anything that is not about Rudolfo's spending | Nothing is recorded |
   | `unknown` | You cannot tell what happened | Nothing is recorded; a question is opened for Rudolfo (send `question` in Portuguese) |

3. Fill only what the notification shows. **Never invent**:
   - `amount` as a canonical decimal (`"32.97"`) and `currency` (BRL when omitted); required for `purchase` and `refund`. If the amount is not shown, use `unknown`.
   - `merchant` as shown; `description` only when it adds something.
   - `payment_method` (`credit_card` or `debit_card`) with `card_last4` only when you know which card it is, for example because Rudolfo told you that the card ending 1234 is his credit card. A credit card purchase stays provisional until the statement; a debit card purchase is confirmed.
   - `occurred_at` only when the text states a purchase time different from `posted_at`.
   - `category`: classify as in the classification policy; it is always a suggestion.
4. **Versions.** `version` above 1 means the phone updated the notification. `previous_versions` shows the earlier texts and your reading of them. If an earlier version became a transaction, send `purchase_update` with `related_transaction_id` set to that transaction, unless the text clearly shows a different purchase.
5. **Purchases already recorded.** Send `purchase` without `related_transaction_id` first. When a transaction with the same amount and a close date exists (from the conversation or a statement), Lastro answers `possible_match` with candidates, their `reasons` and their `differences` (see `get_instructions` topic `matching`). Then:
   - same purchase (same amount, same day, compatible merchant): send the item again with `related_transaction_id`. Lastro links the notification and fills the card, the exact time and missing fields, and keeps what Rudolfo said;
   - a different purchase: send it again with `confirm_distinct: true`;
   - not sure: ask Rudolfo. Two purchases of the same amount on the same day can both be real. If you are processing the inbox without him, send it with `defer_match: true`: it is recorded apart, pending clarification, with a `possible_match` question for him.
6. Send everything in one `submit_notification_interpretations` call (up to 50 items). Each item is applied on its own.
7. Tell Rudolfo briefly what was recorded, using each `summary`, and ask the open questions.

## Reading the answer

Each item comes back with `outcome` and `result`:

- `applied`: done. `effect` says what happened (`created`, `linked`, `updated`, `voided`, `refund_created`, `none`).
- `already_applied`: you had sent the same reading before; nothing changed. Resending a batch is always safe.
- `possible_match` (`outcome: "pending"`): nothing was recorded; see step 5.
- `outcome: "failed"`: nothing was applied for that item. Fix what `error.message` says and send it again.
  - `already_interpreted`: this version already has a different reading. If your reading changed (for example Rudolfo said it was only an alert), send it again with `reinterpret: true` and a short `reason` in Portuguese. The earlier effect is reverted: a purchase created from it is voided, and an update corrects the same transaction.
  - `reinterpretation_not_supported`: fix the transactions with `update_transaction` or `void_transaction`.

## Tracking and coverage

- Automatic tracking starts **disabled**. Turn it on, pause or resume it with `set_tracking` only when Rudolfo asks. Send `inbox_interval_minutes` with the interval of his routine, so a late inbox is flagged at twice that interval. While paused, the phone's notifications are dropped and everything already recorded stays available.
- Before answering how much Rudolfo spent in a period, call `get_coverage_status` for those days. If `complete` is false, tell him each item of `limitations` together with the numbers. Never say there was no spending only because no notification arrived.

## Never

- Never treat `removed: true` as a cancellation: Rudolfo only dismissed it on the phone.
- A `declined` payment never changes a transaction. If Rudolfo told you about that purchase in the conversation and it was declined, ask him whether it went through later; void the record only if it did not.
- Never record a notification through `record_expense`; it would count the purchase twice.
- Never add up amounts yourself.
