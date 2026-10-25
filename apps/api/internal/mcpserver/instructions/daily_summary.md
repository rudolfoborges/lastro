# Daily summary

Every morning you send Rudolfo a short summary of the previous day's purchases. Lastro computes it and remembers which days were sent; it never sends messages itself. The summary is off until Rudolfo asks for it.

## Turning it on or off

- Only when Rudolfo asks ("ativa o resumo diário às 8h", "para de mandar o resumo"), call `set_daily_summary` with `enabled` and, when he says one, `time` (`HH:MM`, his time zone).
- Then create or remove your own daily scheduled task at that time, in his time zone (`settings.timezone`). Changing the time means moving the task.

## The routine

At the scheduled time, every day:

1. Call `get_daily_summary` without `day`: it covers yesterday in Rudolfo's time zone.
2. If `settings.enabled` is false, stop: send nothing.
3. If `delivered` is true, stop: that day was already sent.
4. Send Rudolfo the `summary`, as it is or lightly shortened, keeping:
   - the coverage limitations, before any number. A day with no records and incomplete coverage is never "no spending";
   - confirmed, provisional and pending apart. Never add them up, never add currencies, never compute totals yourself;
   - the open questions, when there are any, with an offer to resolve them.
5. If Rudolfo asked for the summary with a chart, draw `charts` as the `queries` procedure says, with its notes.
6. Right after sending, call `mark_daily_summary_delivered` with the same `day`.

If the routine did not run on some day, do not catch up on old days unless Rudolfo asks; when he does, call `get_daily_summary` with that `day`.

## When Rudolfo asks

"Como foi ontem?" or "manda o resumo de terça" can be answered with `get_daily_summary` (with `day` when it is not yesterday), even when the summary is off or the day was already delivered. Mark the day as delivered only when it is the scheduled routine that sent it.
