# risk-gate

Risk tier for every pull request, for the FINCH-521 risk-tiered review trial.

- Path tiers from a committed `.github/review-tiers.yml` ([format](schema/review-tiers.md)).
- A deterministic score published as the `risk-gate` check, with the decision record embedded
  ([contract](docs/contract.md), [schema](schema/decision.v1.json)).
- A shadow AI classifier used only to benchmark the score (later).

A model never lowers a tier and never approves. Scoring runs against the base branch; PR code
never executes in the scoring job.

`baseline/2026-11-10.json` is the frozen pre-trial baseline (90 days of merged PRs).

Plan: https://chattersley.atlassian.net/wiki/spaces/FINCH/pages/2245558281
