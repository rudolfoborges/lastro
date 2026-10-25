# Architecture Principle

All communication with the user goes through Case, the user's AI assistant, over MCP. Case understands and classifies; the server stores, validates and computes.

- The server never interprets or classifies: no PDF parsing, no notification text parsing, no category heuristics, no automatic cross-source matching. Tools receive data Case already understood, validate it and reject it with clear errors.
- Wallet notifications arrive from the user's own Android app and are stored as `pending_interpretation`. A periodic routine in Case fetches them, interprets and classifies them, and submits the interpretation back.
- Case decides when evidence from different sources is the same purchase; the server only offers explainable candidates and rejects merges that break invariants.
- Statement PDFs never reach the server: Case reads them and submits only the structured, classified extraction, with page and excerpt anchors for each item. A resubmitted statement is identified by issuer + card + cycle.
- The server owns integrity: idempotency, deduplication, invariants (no double counting, statement composition vs. informed total), totals and views, revision history and audit.

# Code Conventions

## Language

- All code is written in English, including the domain: identifiers, entities, fields, enum values, database tables and columns, API routes and payloads, MCP tool names, descriptions, schemas and server instructions, log and error messages, and test names.
- The PRD (`docs/prd.md`) is written in Portuguese. Translate business terms with the glossary below instead of inventing new names, and extend the glossary when a new term appears.
- Only user-facing content stays in pt-BR: text that Case relays to Rudolfo and display labels such as category names. It is data, not code.

## Comments

- Avoid comments. Prefer clear names, small functions and tests that document behavior.
- Write a comment only when the code cannot express the *why* (non-obvious constraint, workaround, external quirk), and always in English.
- Do not add doc comments just to satisfy linters; lint rules that require them stay disabled.

## Domain Glossary (PRD pt-BR → code)

| PRD term | Code |
|---|---|
| lançamento (compra ou movimentação) | `transaction` |
| observação / evidência | `observation` |
| vínculo entre lançamento e evidência | `transaction_evidence` |
| interpretação feita pela Case / pendente de interpretação | `interpretation` / `pending_interpretation` |
| fusão de lançamentos da mesma compra | `merge` (status `merged`, `merged_into_id`) |
| leitura da fatura enviada pela Case | `statement_extraction` |
| situação: provisória / confirmada / pendente de esclarecimento | `status`: `provisional` / `confirmed` / `pending_clarification` |
| natureza: despesa, encargo, crédito, estorno, pagamento, transferência, ajuste | `nature`: `expense`, `charge`, `credit`, `refund`, `payment`, `transfer`, `adjustment` |
| estabelecimento / beneficiário | `merchant` / `payee` |
| meio de pagamento | `payment_method` |
| modalidade (ex.: Pix parcelado) | `payment_modality` (ex.: `pix_installment`) |
| categoria / divisão entre categorias | `category` / `category_allocation` |
| finalidade | `purpose` |
| fatura / versão da fatura / item da fatura | `statement` / `statement_version` / `statement_line` |
| fechamento / vencimento / emissão | `closing_date` / `due_date` / `issue_date` |
| saldo anterior | `previous_balance` |
| conferência da fatura (conferida) | `statement_review` (`reconciled`) |
| correspondência entre fontes | `match` |
| candidato a correspondência: motivos e diferenças / compra não encontrada na fatura | `match_candidate`: `reasons` / `differences` / `unmatched` |
| confirmar, rejeitar, desfazer correspondência / decidir depois / par rejeitado | `confirm_match`, `reject_match`, `undo_match` / `defer_match` / `match_rejection` |
| parcelamento / parcela | `installment_plan` / `installment` |
| parcela cobrada / futura conhecida / estimada; valor conhecido só da parcela | `installment` `state`: `charged` / `known_future` / `estimated`; `amount_basis`: `installment` / `total` |
| compromissos futuros | `future_commitments` |
| principal, juros, IOF, tarifa | `principal`, `interest`, `iof`, `fee` |
| componente da cobrança (contido na parcela ou cobrado à parte) / composição não informada | `charge_component` (`included`) / `missing_breakdown` |
| dúvida | `clarification` |
| regra de classificação / condições / sugestão de regra aprovada por Rudolfo | `classification_rule` / `conditions` (`min_amount`, `max_amount`, `card_last4`, `payment_method`) / status `suggested` → `active` (`approve`) |
| histórico de alterações | `revision` |
| acompanhamento automático / cobertura / lacuna | `tracking` / `coverage` / `gap` |
| período de acompanhamento / sinal de vida do celular / entrega atrasada | `tracking_period` / `heartbeat` / `late_delivery` |
| precisão da data / origem da data | `date_precision` (`exact`, `day`, `month`, `unknown`) / `date_source` (`stated`, `inferred`, `document`) |
| "agora", "hoje", "ontem" | `date_reference` (`now`, `today`, `yesterday`) |
| fuso horário da conta | `timezone` |
| valor em centavos / moeda | `amount_minor` / `currency` |
| redação de dados sensíveis | `redaction` |
| lançamento anulado (registro por engano) | `status` `voided` |
| origem da evidência: conversa, notificação, linha de fatura, manual, importação | `source`: `conversation`, `wallet_notification`, `statement_line`, `manual`, `import` |
| situação do vínculo: sugerido, confirmado, rejeitado, desfeito | `transaction_evidence.status`: `suggested`, `confirmed`, `rejected`, `undone` |
| quem fez: sistema, Case, regra, Rudolfo | `system`, `agent`, `rule`, `user` |
| proveniência por campo | `field_provenance` |
| categoria sugerida / confirmada / ausente | `allocation status`: `suggested` / `confirmed` / `missing` |
| chave de idempotência / estado da operação: concluída, pendente, falhou | `idempotency_key` / `outcome`: `completed`, `pending`, `failed` |
| autorização dada a um aplicativo (OAuth) | `oauth_grant` |
| cartão de crédito, cartão de débito, Pix, dinheiro | `credit_card`, `debit_card`, `pix`, `cash` |
| tipo do evento: compra, atualização, cancelamento, estorno, recusa, aviso, sem relação, incerto | `event_type`: `purchase`, `purchase_update`, `cancellation`, `refund`, `declined`, `notice`, `unrelated`, `unknown` |
| efeito da interpretação: nenhum, criou, vinculou, atualizou, anulou, criou estorno, desfeito | `effect`: `none`, `created`, `linked`, `updated`, `voided`, `refund_created`, `undone` |
| dispositivo / versão da notificação / caixa de entrada | `wallet_device` / `wallet_notification_versions` / `inbox` |
| avaliação da Case / conta de avaliação / taxa de acerto por fluxo | `case_evaluation` / sandbox account (`sandbox_of`) / `flows` (`passed` / `total`) |
| resumo diário / horário / dia entregue | `daily_summary` / `time` (`HH:MM`) / `delivered` (`daily_summary_deliveries`) |
| gráfico / série / rótulo / observações do gráfico | `chart` / `series` / `labels` / `notes` |
| critério de aceite / suíte de aceitação | acceptance `criterion` / `acceptance` suite (`TestCriterionNN…`) |

<!-- nx configuration start-->
<!-- Leave the start & end comments to automatically receive updates. -->

# General Guidelines for working with Nx

- For navigating/exploring the workspace, invoke the `nx-workspace` skill first - it has patterns for querying projects, targets, and dependencies
- When running tasks (for example build, lint, test, e2e, etc.), always prefer running the task through `nx` (i.e. `nx run`, `nx run-many`, `nx affected`) instead of using the underlying tooling directly
- Prefix nx commands with the workspace's package manager (e.g., `pnpm nx build`, `npm exec nx test`) - avoids using globally installed CLI
- You have access to the Nx MCP server and its tools, use them to help the user
- For Nx plugin best practices, check `node_modules/@nx/<plugin>/PLUGIN.md`. Not all plugins have this file - proceed without it if unavailable.
- NEVER guess CLI flags - always check nx_docs or `--help` first when unsure

## Scaffolding & Generators

- For scaffolding tasks (creating apps, libs, project structure, setup), ALWAYS invoke the `nx-generate` skill FIRST before exploring or calling MCP tools

## When to use nx_docs

- USE for: advanced config options, unfamiliar flags, migration guides, plugin configuration, edge cases
- DON'T USE for: basic generator syntax (`nx g @nx/react:app`), standard commands, things you already know
- The `nx-generate` skill handles generator discovery internally - don't call nx_docs just to look up generator syntax


<!-- nx configuration end-->
