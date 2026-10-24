-- +goose Up
-- Until now a suggested "unidentified" category closed the category question.
-- The questions are worded like ledger.describe so they read like the ones the
-- service opens.
WITH current_category AS (
    SELECT DISTINCT ON (a.transaction_id) a.transaction_id, c.code, a.status
    FROM category_allocations a
    JOIN categories c ON c.account_id = a.account_id AND c.id = a.category_id
    WHERE a.superseded_at IS NULL
    ORDER BY a.transaction_id, a.amount_minor DESC, c.code
),
pending AS (
    SELECT t.id, t.account_id, t.description, t.date_precision, t.occurred_on, t.currency, t.amount_minor,
           'category_unknown|transaction|transaction:' || t.id AS dedup_key
    FROM transactions t
    JOIN current_category cc ON cc.transaction_id = t.id
    WHERE cc.code = 'unidentified' AND cc.status = 'suggested'
      AND t.nature IN ('expense', 'charge', 'refund')
      AND t.status NOT IN ('voided', 'merged')
)
INSERT INTO clarifications (id, account_id, kind, transaction_id, subject, dedup_key, question, status)
SELECT uuidv7(), p.account_id, 'category_unknown', p.id, 'transaction', p.dedup_key,
       format('Qual a categoria de "%s" (%s %s, %s)?',
           p.description,
           CASE p.currency
               WHEN 'BRL' THEN 'R$' WHEN 'USD' THEN 'US$' WHEN 'EUR' THEN '€' WHEN 'GBP' THEN '£'
               WHEN 'JPY' THEN 'JP¥' WHEN 'AUD' THEN 'AU$' WHEN 'CAD' THEN 'CA$'
               ELSE p.currency
           END,
           CASE WHEN p.currency IN ('CLP', 'JPY', 'KRW', 'PYG')
               THEN translate(to_char(p.amount_minor, 'FM999,999,999,999,999,990'), ',', '.')
               ELSE translate(to_char(p.amount_minor / 100.0, 'FM999,999,999,999,999,990.00'), ',.', '.,')
           END,
           CASE p.date_precision
               WHEN 'unknown' THEN 'data desconhecida'
               WHEN 'month' THEN to_char(p.occurred_on, 'MM/YYYY')
               ELSE to_char(p.occurred_on, 'DD/MM/YYYY')
           END),
       'open'
FROM pending p
WHERE NOT EXISTS (
    SELECT FROM clarifications c
    WHERE c.account_id = p.account_id AND c.dedup_key = p.dedup_key AND c.status IN ('open', 'dismissed')
);

-- +goose Down
-- The previous release closes these questions the next time each transaction changes.
SELECT 1;
