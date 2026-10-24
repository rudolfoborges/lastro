# Case integration kit

Case is Rudolfo's AI assistant and runs on ChatGPT. Case talks to Rudolfo, understands and classifies what he says, and calls Lastro's MCP tools; Lastro stores, validates and computes. This kit is what Case needs to use Lastro correctly, and how to connect it.

## Connection

> **Temporary URL.** Until the certificate of `lastro.code04.dev` is issued, Lastro is served and announces itself (`PUBLIC_BASE_URL`, the OAuth issuer) at `https://api-production-9ee63.up.railway.app`. Switching back to `lastro.code04.dev` means changing `PUBLIC_BASE_URL` in `.railway/railway.ts`, applying it, and in ChatGPT changing the app's URL and authorizing again: tokens are bound to the old address and stop working.

| Item | Value |
| --- | --- |
| MCP server URL | `https://api-production-9ee63.up.railway.app/mcp` |
| Transport | Streamable HTTP |
| Authentication | OAuth 2.1 (ChatGPT discovers it from the server) |
| Scopes Case needs | `expenses:read`, `expenses:write`, `statements:write` |

### First time: create the account

1. Ask the operator for an invite (`lastro invites create`, see [operations](operations.md)).
2. Sign up and keep the `admin` key in a password manager; it is shown only once:

   ```bash
   curl -X POST https://api-production-9ee63.up.railway.app/v1/accounts \
     -d '{"invite_code": "inv_…", "name": "Rudolfo"}'
   ```

### Connect ChatGPT

ChatGPT's labels change over time; the steps are what matters.

1. In ChatGPT, open **Settings → Apps** (formerly Connectors) → **Advanced settings** and turn on **Developer mode**.
2. **Create** an app (custom MCP connector):
   - name: `Lastro`;
   - MCP server URL: `https://api-production-9ee63.up.railway.app/mcp`;
   - authentication: **OAuth**. Leave client ID and secret empty: ChatGPT identifies itself with its client metadata document.
3. ChatGPT opens Lastro's authorization page, "ChatGPT quer acessar o Lastro". Check the host shown is `chatgpt.com`, keep the permissions you want, paste the **admin key** and click **Autorizar**. The key only proves the account is yours; ChatGPT receives its own access, which you can revoke.
4. Back in ChatGPT, the app lists Lastro's tools. Create a **project** for Case, enable the Lastro app in it, and paste the [project instructions](#project-instructions) below.
5. ChatGPT asks for confirmation before write actions. Approving them for the Lastro app avoids a prompt on every expense.

To disconnect ChatGPT, delete the app there or revoke its access in Lastro. Revoking the `admin` key used on the consent page also revokes the access it granted.

```bash
curl -H "Authorization: Bearer $ADMIN" https://api-production-9ee63.up.railway.app/v1/oauth/grants
curl -X POST -H "Authorization: Bearer $ADMIN" https://api-production-9ee63.up.railway.app/v1/oauth/grants/<id>/revoke
```

### Other MCP clients

Clients that can send a header (MCP Inspector, Claude Code, scripts) use an API key instead of OAuth. Create one with only the scopes needed:

```bash
curl -X POST -H "Authorization: Bearer $ADMIN" https://api-production-9ee63.up.railway.app/v1/api-keys \
  -d '{"name": "inspector", "scopes": ["expenses:read"]}'
```

and send `Authorization: Bearer lst_…` to `/mcp`.

## Project instructions

Paste this into the instructions of Case's project in ChatGPT. ChatGPT does not read MCP prompts or the server's `instructions`, so this text and the `get_instructions` tool are how Case learns the procedures.

```text
You are Case, Rudolfo's personal assistant for expenses. Always talk to Rudolfo in Brazilian Portuguese.

Lastro (the "Lastro" app) is the record of Rudolfo's expenses. You understand and classify; Lastro stores, validates and computes. Before recording or correcting anything in a conversation, call get_instructions with topic "conversation"; before classifying, call it with topic "classification". Follow those procedures exactly.

Rules that always apply:
- Never invent amounts, dates, merchants, categories or purposes. Leave unknown fields out.
- When Rudolfo reports a purchase, record it right away with record_expense. Ask first only if the amount or which purchase it is is unclear.
- Generate a new request_id for each expense and reuse it on every retry of that same expense.
- Send amounts as decimals with a dot ("327.39"). Use date_reference "now", "today" or "yesterday" for relative dates.
- Classify with list_categories and send category_status "suggested"; use "confirmed" only when Rudolfo said the category.
- After each call, tell Rudolfo what was recorded and what is pending, using the summary. If the outcome is "failed", nothing was recorded: say so and fix the call. If the result is "possible_duplicate", ask whether it is the same purchase before calling again with confirm_distinct.
- Use update_transaction, set_category and void_transaction for corrections, always with Rudolfo's reason, and list_clarifications / resolve_clarification for pending questions.
- Before talking about the spending of a period, call get_coverage_status and tell Rudolfo every limitation it lists. Turn automatic tracking on or off with set_tracking only when he asks.
- At the start of each conversation, call list_pending_notifications. If anything is pending, read get_instructions topic "notifications" and process the Google Wallet inbox before answering. Never record a notification with record_expense.
- When Rudolfo sends a credit card statement PDF, read get_instructions topic "statements", read every page yourself and send only the structured reading with submit_statement_extraction. Never send the PDF to Lastro and never record statement lines with record_expense.
- Never add up amounts yourself.
```

## Conversation procedure

The full procedure is served by `get_instructions` (topic `conversation`, source in `apps/api/internal/mcpserver/instructions/conversation.md`) and the classification policy by topic `classification`. In short:

1. **When to ask**: only when the amount or the identity of the purchase is unclear. Missing payment method, category, date or purpose is recorded as pending.
2. **How to confirm**: relay the `summary`, mention what was inferred (`inferred`) and what is still missing (`pending`). Never say something was recorded unless `result` is `recorded` or `duplicate_of`.
3. **`request_id` and time**: one `request_id` per expense, reused on retries; send `message_at` only when the time of the message is known.
4. **Voice**: send the transcription as `raw_text` with `input_mode: "voice"`.
5. **Classify before recording**: `category` with `category_status: "suggested"`; `confirmed` only when Rudolfo said it.

## Dialog examples

The tool arguments are abbreviated; Case always sends a fresh `request_id` per expense.

**Complete record**

> Rudolfo: Gastei R$ 327,39 no mercado agora, no crédito final 1234.
>
> Case → `record_expense {raw_text, amount: "327.39", description: "Compra no mercado", date_reference: "now", payment_method: "credit_card", card_last4: "1234", category: "groceries"}`
>
> Case: Registrei R$ 327,39 em "Compra no mercado", categoria Mercado (sugerida), hoje. Fica provisória até aparecer na fatura.

**Partial record**

> Rudolfo: Paguei 45 reais de almoço.
>
> Case → `record_expense {amount: "45", description: "Almoço", date_reference: "today", category: "meals"}`
>
> Case: Registrei R$ 45,00 em "Almoço", categoria Alimentação e refeições (sugerida). Falta o meio de pagamento: foi cartão, Pix ou dinheiro?

**Asking before recording**

> Rudolfo: Comprei umas coisas na farmácia.
>
> Case → `record_expense {raw_text, description: "Compras na farmácia", category: "health"}` → `needs_clarification`, "Qual foi o valor?"
>
> Case: Quanto deu a compra na farmácia?

**Later correction**

> Rudolfo: Aquela do mercado foi R$ 327,93, errei.
>
> Case → `update_transaction {transaction_id, amount: "327.93", reason: "Rudolfo corrigiu o valor"}`
>
> Case: Atualizei o valor de "Compra no mercado" para R$ 327,93. O valor original fica guardado no histórico.

**Repeated message**

> (The same message arrives again with a new `request_id`.)
>
> Case → `record_expense` → `possible_duplicate`
>
> Case: Já registrei R$ 327,39 no mercado hoje às 23:50. É a mesma compra ou foi outra?
>
> Rudolfo: Outra, voltei lá.
>
> Case → `record_expense {… same request_id …, confirm_distinct: true}` → `recorded`

**Pix without purpose**

> Rudolfo: Fiz um Pix de 150 pra Ana.
>
> Case → `record_expense {amount: "150", description: "Pix para Ana", payment_method: "pix", date_reference: "now"}` → confirmed, a purpose question is open
>
> Case: Registrei o Pix de R$ 150,00 para Ana. Para que foi?
>
> Rudolfo: Presente de aniversário.
>
> Case → `resolve_clarification {clarification_id, action: "resolve", note: "Presente de aniversário", purpose: "presente de aniversário"}`

## Google Wallet inbox

Notifications captured by the Android app wait in Lastro as pending until Case interprets them (`list_pending_notifications`, then `submit_notification_interpretations`). The procedure is served by `get_instructions` topic `notifications` (source in `apps/api/internal/mcpserver/instructions/notifications.md`).

Lastro does not depend on what triggers the processing. Two triggers, which can be combined:

- **At the start of each conversation:** the project instructions above tell Case to check the inbox first. It always works, but a purchase only shows up after Rudolfo talks to Case.
- **A scheduled task in ChatGPT** with a prompt such as "Processe as notificações pendentes do Lastro seguindo get_instructions topic notifications." The interval defines how close to the purchase it appears. ChatGPT's documentation does not say whether scheduled tasks can call write tools of an MCP app, and in October 2026 developers reported such writes being blocked inside tasks. Check it with one real notification before relying on it (decided on #4: Rudolfo takes care of the trigger).

Either way, the coverage status (#30) shows how long the inbox has been waiting.

> Case: Antes de tudo: o Wallet avisou de 2 compras. Registrei R$ 32,97 na Padaria Pão Quente (refeições, sugerida) e R$ 89,90 no Posto Avenida (combustível, sugerida). Os dois no crédito final 1234, provisórios até a fatura.

## Credit card statements

Rudolfo sends the monthly statement PDF to Case in the conversation. Case reads it (every page, dates from the content), and submits only the structured reading with `submit_statement_extraction`: identity, dates, printed total, previous balance and every line with its page and excerpt. The procedure is served by `get_instructions` topic `statements` (source in `apps/api/internal/mcpserver/instructions/statements.md`); the contract is `apps/api/docs/statement-extraction.schema.json`.

> Rudolfo: (envia a fatura em PDF)
>
> Case → `get_instructions {topic: "statements"}`, then `submit_statement_extraction {issuer, card_last4, closing_date, due_date, total, previous_balance, reading: "complete", items: [...]}`
>
> Lastro → `possible_match`: o item 1 (R$ 32,97, Padaria Exemplo, 14/09) pode ser a compra que Rudolfo contou na conversa.
>
> Case → envia a leitura de novo com `related_transaction_id` no item 1.
>
> Case: Registrei a fatura de outubro do cartão final 1234: 12 itens, 9 lançamentos novos, a padaria que você já tinha me contado agora está confirmada, e 3 itens ficam só na fatura (2 parcelas e 1 crédito que não entendi). Uma dúvida: o que é o crédito "AJ CRED 0001" de R$ 12,34 em 28/09?

## Scheduled routines

Later work without a request from Rudolfo, such as the daily summary, follows the same rule: Lastro computes, and a ChatGPT scheduled task or the next conversation delivers it.

## Manual test checklist (production)

Run with Case in ChatGPT after connecting it. Use real but harmless expenses and void them at the end if they were only tests.

- [ ] Case reads `get_instructions` (conversation) before the first record.
- [ ] "Gastei R$ 10,00 de café agora" is recorded with the right amount, a suggested category and today's date marked as inferred; the answer mentions the missing payment method.
- [ ] Repeating the same sentence makes Case ask whether it is the same purchase, and nothing is recorded until the answer.
- [ ] "Foi no débito" completes the payment method and the record becomes confirmed.
- [ ] "Na verdade foi R$ 12,00" corrects the amount; `get_transaction` shows the old value and the reason in the history.
- [ ] "Isso foi lanche, não refeição" sets a confirmed category.
- [ ] A Pix without purpose shows up in "quais dúvidas eu tenho?" and is resolved by answering it.
- [ ] "Apaga esse registro, foi engano" voids it; it disappears from the listing and stays in the history.
- [ ] Revoking the grant in `/v1/oauth/grants` makes the next call from ChatGPT ask for authorization again.
- [ ] "Ativa o acompanhamento do Wallet" calls `set_tracking` and the phone's next heartbeat answers `active`; "pausa o acompanhamento" pauses it and the records stay listed.
- [ ] "Como está a cobertura desta semana?" lists the gaps and the inbox state from `get_coverage_status`.
- [ ] With a real Wallet notification pending, a new conversation makes Case process the inbox first and report the purchase; resending the same interpretation changes nothing.
- [ ] A scheduled task that processes the inbox actually records the purchase (if not, keep only the per-conversation trigger).
