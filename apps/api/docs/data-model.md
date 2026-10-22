# Data model

Every table below belongs to one account: it has `account_id`, row-level security keyed by `current_account_id()`, and is reached through `Store.TenantTx`. References between tables of the same account are composite foreign keys on `(account_id, id)`, so a row can never point at another account's data. The application role cannot `DELETE` from any table; changes are recorded through status columns and the revision history.

```mermaid
erDiagram
    accounts ||--o{ payment_methods : has
    accounts ||--o{ observations : receives
    accounts ||--o{ transactions : owns
    observations ||--o{ observation_interpretations : "is read as"
    observations ||--o{ transaction_evidence : supports
    transactions ||--o{ transaction_evidence : "is supported by"
    transactions ||--o{ field_provenance : "explains fields with"
    observations |o--o{ field_provenance : "is the source of"
    payment_methods |o--o{ transactions : pays
    payment_methods |o--o{ observation_interpretations : mentions
    transactions |o--o{ transactions : "refund_of / merged_into"

    payment_methods {
        uuid id PK
        text kind "credit_card, debit_card, pix, cash"
        text card_last4 "cards only"
        text issuer
        text network
        text nickname
    }
    observations {
        uuid id PK
        text source "conversation, wallet_notification, statement_line, manual, import"
        text source_key "unique per account and source"
        jsonb payload "redacted raw evidence"
        jsonb redactions "kind and count, never the values"
        bytea fingerprint "content fingerprint for repetition checks"
        timestamptz received_at
    }
    observation_interpretations {
        uuid id PK
        uuid observation_id FK
        int version "1, 2, ... per observation"
        text event_type "purchase, purchase_update, cancellation, refund, declined, notice, unrelated"
        bigint amount_minor
        char currency
        text merchant
        timestamptz occurred_at
        date occurred_on
        text date_precision
        text date_source
        uuid payment_method_id FK
        jsonb details
        text author "agent, user, system"
    }
    transactions {
        uuid id PK
        text original_description
        text description
        text merchant
        text payee
        bigint amount_minor "always positive"
        char currency
        bigint original_amount_minor "foreign purchases"
        char original_currency
        timestamptz occurred_at "exact precision only"
        date occurred_on "local date, null when unknown"
        text date_precision "exact, day, month, unknown"
        text date_source "stated, inferred, document"
        uuid payment_method_id FK
        text nature "expense, charge, credit, refund, payment, transfer, adjustment"
        text status "provisional, confirmed, pending_clarification, voided, merged"
        text purpose
        int installment_count
        bigint installment_amount_minor
        uuid refund_of_id FK
        uuid merged_into_id FK
    }
    transaction_evidence {
        uuid id PK
        uuid transaction_id FK
        uuid observation_id FK
        text status "suggested, confirmed, rejected, undone"
        text origin "system, agent, user"
        smallint score "0 to 100"
        text_array reasons
    }
    field_provenance {
        uuid transaction_id PK
        text field PK
        uuid observation_id FK
        bool confirmed_by_user
    }
```

## Rules the schema enforces

- **Evidence is immutable.** `observations` and `observation_interpretations` have no `UPDATE` privilege. A new reading of the same evidence is a new interpretation `version`; an observation without any interpretation is `pending_interpretation`.
- **One source identity, one observation.** `(account_id, source, source_key)` is unique: a repeated conversation message, notification or statement line cannot create a second observation.
- **No double counting.** An observation can be `confirmed` evidence of at most one transaction at a time (`transaction_evidence_confirmed_observation_idx`), and a transaction has at most one open link to the same observation. Links are never removed: they move to `rejected` or `undone`.
- **Amounts are positive magnitudes.** The `nature` gives the direction: `credit` and `refund` bring money back; the others take it out. Foreign purchases keep the original amount and currency next to the charged one; the two never share a currency.
- **Dates keep their precision.** `occurred_at` exists only for `exact`; `occurred_on` is null only for `unknown`; `month` dates sit on the first day of the month.
- **Merges keep both sides.** `status = 'merged'` exactly when `merged_into_id` is set, so the merged transaction and its evidence remain queryable and the merge can be undone.
- **Field provenance.** For each field of a transaction, `field_provenance` names the observation it came from and whether Rudolfo confirmed it; confirmed fields are protected from automatic overwrites. Categories carry their own status and origin in `category_allocations`.
