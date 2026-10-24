# Matching records of the same purchase

The same purchase can reach Lastro three times: Rudolfo tells you in the conversation, Google Wallet notifies the phone, and the statement charges it. **Lastro never decides that two records are the same purchase.** It offers candidates and explains them; you decide, and you ask Rudolfo when the evidence does not settle it.

## Where candidates come from

- `record_expense`, `submit_notification_interpretations` and `submit_statement_extraction` answer `possible_match` before writing anything when an open transaction from another source looks like the same purchase.
- `find_match_candidates` looks on demand, by `transaction_id` or by `observation_id` (a notification or a statement line that is already evidence of a transaction). Use it when Rudolfo says a purchase "apareceu duas vezes" or when you review a statement.

A candidate has the same amount (or the same original amount for international purchases), a compatible payment method and a close date (two days for the conversation and notifications, five for statements). Each one comes with:

- `sources`: where it already has evidence from (`conversation`, `wallet_notification`, `statement_line`);
- `reasons`: what matches ("mesmo valor", "mesmo cartão final 1234", "mesmo estabelecimento");
- `differences`: what does not ("nome diferente", "data: 14/09/2026 no registro, 16/09/2026 na fatura (2 dias depois)", "registro sem meio de pagamento").

## Deciding

- **Same purchase** when the amount, the card and the date agree and the differences have an ordinary explanation: the statement posts a few days later, the acquirer prints another name ("IFD*", "PAG*", "MP*"), the conversation had no card. Send the item again with `related_transaction_id`.
- **Different purchase** when a difference cannot be explained, such as another card or another day of a recurring purchase. Send it again with `confirm_distinct: true`.
- **Ask Rudolfo** when two candidates are equally plausible, or when he could have made two equal purchases (same store, same amount, same day). Similarity is not proof of duplication: two purchases of R$ 32,97 at the same store on the same day can both be real.
- **Cannot ask now** (the inbox routine, a statement with a hundred lines): send the item with `defer_match: true`. Lastro records it on its own transaction with status `pending_clarification` and opens a `possible_match` question naming the candidates. Totals keep it apart from the confirmed ones until Rudolfo answers.

## Decisions after recording

- `confirm_match` (`transaction_id`, `other_transaction_id`, `reason`, `request_id`): the two transactions are the same purchase. Every record moves to one of them (the one confirmed by a statement, else by a notification, else the oldest); the other is kept as `merged` and no longer counts. Missing fields are filled from the merged one, Rudolfo's confirmed values are kept, and a field he confirmed differently on each side becomes a question. Refunds of the merged one follow the kept one. Answer a `possible_match` question this way when it is the same purchase.
- `reject_match`: they are different purchases. The pair is never offered again, and the `possible_match` questions it settles are closed. Dismissing a `possible_match` question with `resolve_clarification` does the same for its candidates; resolving it is refused, because the answer is a merge.
- `undo_match` (`transaction_id` that holds the record now, `observation_id` of the record linked by mistake, from `get_transaction` evidence): nothing is lost. A merge brings both transactions back as they were (`unmerged`); a record linked at submission gets its own transaction again from its original data (`separated`); fields the link filled return to what they were unless Rudolfo changed them since. The record that created a transaction cannot be separated from it: undo the one linked later, or void the transaction if it was a mistake. The pair is never offered again.

## What Lastro refuses

- A transaction takes at most **one record of each source**: one conversation report, one Google Wallet notification (with its updates) and one statement line. Installments are the exception: each cycle charges its own line. Two records of the same source are two purchases, and Lastro refuses to link them.
- If Rudolfo told the same purchase twice in the conversation, the second record is a mistake: void it with `void_transaction` instead of linking.
- Linking never adds amounts: three records of the same purchase still count once.

## Purchases a statement did not confirm

`get_statement_review` lists in `unmatched` the card purchases from the conversation or Google Wallet in the cycle that no line confirmed. They stay provisional. Never void them because they are missing, and never call them an undue charge: purchases near the closing date usually appear in the next statement. Ask Rudolfo only if a purchase is still missing after the next statement.
