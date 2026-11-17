# Evals

## routing.json — intent→workflow behavioral cases

30 cases (27 positive + 3 negative-with-owner) pinning the router's
free-text routing: CJK + English intents per workflow, the fix-vs-debug
repair-bias disambiguation rule (skills/summoner/SKILL.md §When to Use),
and three negative cases that used to be plausible mis-routes.

Run (opt-in, needs claude CLI + model auth — NOT a CI gate):

```bash
scripts/run-routing-evals.sh
```

Conventions:
- Baseline is measured-current: the first full run's score is the number to
  defend; there is no aspirational threshold. **Measured 2026-09-03: 28/30
  stable-correct, 2 borderline watch** (`fix-02` "帮我排查这个 bug" and
  `ship-02` "review before merge" flip run-to-run even with the
  disambiguation rule present at every routing-read layer — SKILL.md,
  both command frontmatter descriptions, and plugin.json).
- A failing case means either the when_to_use / disambiguation text drifted
  (fix the text) or the expectation is wrong (fix the case) — decide which
  before committing.
- Keep the corpus small; expand only after an observed mis-route (speculative
  evals rot as fast as speculative docs).
