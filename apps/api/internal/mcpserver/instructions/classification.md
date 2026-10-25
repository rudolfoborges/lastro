# Classification policy

You classify every expense before recording it. Lastro never guesses a category: it stores yours as `suggested` and only Rudolfo turns it into `confirmed`.

## Taxonomy

Use the codes returned by `list_categories`. The defaults are:

| Code | Label | Use for |
| --- | --- | --- |
| `groceries` | Mercado | Supermarkets, grocery stores, street markets, butchers |
| `meals` | Alimentação e refeições | Restaurants, lunch, delivery of meals, bakeries when it is a meal |
| `convenience_snacks` | Conveniência — bebidas e snacks | Soft drinks, chocolates, energy drinks and snacks, especially at convenience stores and gas stations |
| `fuel` | Combustível | Gasoline, ethanol, diesel, EV charging |
| `transport` | Transporte | Ride apps, taxi, public transport, tolls, parking |
| `home` | Casa e reforma | Home goods, building materials, repairs, furniture |
| `health` | Saúde | Pharmacy, doctors, exams, health insurance |
| `education` | Educação | Courses, books for study, school |
| `clothing` | Vestuário | Clothes, shoes, accessories |
| `electronics` | Eletrônicos | Devices, gadgets, accessories |
| `software_subscriptions` | Software e assinaturas | Apps, streaming, cloud and software subscriptions |
| `leisure` | Lazer | Entertainment, travel, hobbies |
| `financial_charges` | Encargos financeiros | Interest, IOF, fees, late charges |
| `unidentified` | Não identificado | When you cannot tell |

## Rules

1. **Purpose is not payment modality.** "Pix parcelado", "crédito" or "débito" never explain what the money was for. Purpose is a separate field.
2. **Keep convenience apart.** Soft drinks, chocolates and energy drinks bought at a convenience store are `convenience_snacks`, not `meals`, `groceries` or `fuel`.
3. **Gas stations are not always fuel.** Classify a gas station purchase as `fuel` only when it was fuel; snacks there are `convenience_snacks`; when you cannot tell, ask or use `unidentified`.
4. **Merchants with many activities or marketplaces** (Amazon, Mercado Livre, Americanas, department stores) do not reveal the product. Use what Rudolfo said about the item; without it, `unidentified`.
5. **Suggested versus confirmed.** Your classification is always `suggested`. Send `confirmed` only when Rudolfo said the category himself or explicitly agreed with your suggestion.
6. **One category per purchase.** When a receipt mixes things, use the category of what dominates it.
7. **Corrections stick.** A category Rudolfo confirmed is never replaced by a new suggestion. If a new check disagrees, Lastro opens a clarification instead of changing it.
8. **Rules for the future** apply only when Rudolfo says a guidance must always hold ("sempre que for na padaria X, é café da manhã"). A one-off correction is not a rule. See "Rudolfo's rules" below.
9. **When in doubt, `unidentified`.** A transaction without a category, or with `unidentified` as your suggestion, counts as "Não identificado" and keeps a `category_unknown` question in `list_clarifications` until Rudolfo answers. If he cannot tell either, send `unidentified` as `confirmed`.

## Rudolfo's rules

- **Create** a rule with `create_classification_rule` only when Rudolfo asks for it to always hold. Send the store as he named it, the category, the purpose if he gave one, and his words in `instruction`. A rule never changes past purchases. Each store has at most one active rule.
- **Apply** rules yourself. Before classifying, call `list_classification_rules` (once per conversation or inbox run is enough). When a purchase is at the store of a rule, use the rule's category and send `category_rule_id` (`record_expense`, notification interpretations, statement items) or `rule_id` (`set_category`). The category is then suggested with origin `rule`.
- **When not to apply:** a rule never replaces a category Rudolfo confirmed. If the purchase clearly does not fit the rule (another product at the same store), classify it normally and tell Rudolfo.
- **Archive** a rule with `archive_classification_rule` when Rudolfo says it no longer holds.
