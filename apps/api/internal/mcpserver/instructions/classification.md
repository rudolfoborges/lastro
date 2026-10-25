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

- **Create** a rule with `create_classification_rule` only when Rudolfo asks for it to always hold. Send the store as he named it, the category, the purpose if he gave one, and his words in `instruction`. A rule never changes past purchases.
- **Conditions:** when Rudolfo narrows the rule ("na padaria, até 20 reais é café da manhã", "no cartão final 1234"), send `min_amount` / `max_amount` (inclusive, with `currency` when not BRL), `card_last4` and/or `payment_method`. A store can have several rules with different conditions, but only one with the same conditions.
- **Apply** rules yourself. Before classifying, call `list_classification_rules` (once per conversation or inbox run is enough). When a purchase is at the store of a rule and meets all its `conditions`, use the rule's category and send `category_rule_id` (`record_expense`, notification interpretations, statement items) or `rule_id` (`set_category`). The category is then suggested with origin `rule`. When more than one rule fits, use the one with more conditions. Lastro refuses a rule on a purchase that breaks its conditions (amount, card or payment method): classify that purchase without the rule.
- **When not to apply:** a rule never replaces a category Rudolfo confirmed. If the purchase clearly does not fit the rule (another product at the same store), classify it normally and tell Rudolfo.
- **Archive** a rule with `archive_classification_rule` when Rudolfo says it no longer holds.

## Suggesting rules

- When Rudolfo confirmed the same category for purchases at the same store at least twice, you may propose a rule with `suggest_classification_rule`: the store, the category, the conditions the purchases share (for example, all of them up to some amount, or on one card), a `rationale` in Portuguese and the confirmed purchases in `based_on`. Lastro refuses purchases that are not at that store, do not meet the conditions or whose category Rudolfo did not confirm himself.
- A suggestion never classifies anything. It is listed apart in `list_classification_rules` (`suggestions`); never send its ID as `category_rule_id`.
- Ask Rudolfo with the rationale, in the conversation, never during the inbox routine without him. Only after an explicit yes, call `approve_classification_rule` with his words. When he says no, call `archive_classification_rule` with his reason. Do not suggest the same rule again after a no.
- Pending suggestions show up at the start of a conversation in `list_classification_rules`: ask about them when the moment fits.
