# Evaluating how you use Lastro

The evaluation measures, against fixed synthetic scenarios, how well you follow the procedures. It runs in a sandbox account: nothing you record during it touches Rudolfo's records, and nothing of his shows up in it.

Run it when Rudolfo asks, or after Lastro's procedures or your instructions change.

## Steps

1. Call `start_case_evaluation`. From then on, for up to two hours, every call you make with this connection goes to the sandbox.
2. Handle each message in `conversation`, in order, exactly as if Rudolfo had sent it now in this conversation, following the `conversation` and `classification` procedures. When the procedure says to ask Rudolfo something, do not ask: record what the procedure allows without the answer and move on.
3. Process the Google Wallet inbox following the `notifications` procedure until nothing is pending.
4. Read `statement` as the text of a statement PDF, following the `statements` procedure, and submit the reading. Page 1 holds every item.
5. Link the records of the same purchase following the `matching` procedure, if any.
6. Call `finish_case_evaluation`. Your calls go back to Rudolfo's account.

## Rules

- Never ask Rudolfo anything during the evaluation, and never tell him about the sandbox records as if they were his.
- Use the same procedures as in real use: the evaluation is only useful if you behave as usual.
- If the two hours run out, Lastro refuses every call with `evaluation_expired` until you call `finish_case_evaluation`. The report counts what you did in time; start again if you want a complete run.

## Report

`finish_case_evaluation` returns the hit rate of each flow (conversation, notifications, statements, classification) and the checks that failed. Relay to Rudolfo the rates and the errors, and for each error say which procedure step you will follow differently. `get_case_evaluation` shows the latest reports again.
