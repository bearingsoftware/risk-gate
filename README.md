# risk-gate

Risk tier for every pull request, for the FINCH-521 risk-tiered review trial.

- Path tiers from a committed `.github/review-tiers.yml` ([format](schema/review-tiers.md)).
- A deterministic score published as the `risk-gate` check, with the decision record embedded
  ([contract](docs/contract.md), [schema](schema/decision.v1.json)).
- A shadow AI classifier used only to benchmark the score (later).

A model never lowers a tier and never approves. Scoring runs against the base branch; PR code
never executes in the scoring job.

## Calling risk-gate

Pin the reusable workflow to a commit SHA and pass the same SHA as `risk-gate-ref`:

```yaml
on:
  pull_request:
    types: [opened, synchronize, reopened, ready_for_review, labeled, unlabeled]
    branches: [main]
  pull_request_review:
    types: [submitted, dismissed]
permissions: {}
jobs:
  risk-gate:
    if: github.event.pull_request.base.ref == 'main'
    permissions: {contents: read, pull-requests: write, checks: write}
    uses: bearingsoftware/risk-gate/.github/workflows/risk-gate.yml@<sha> # main
    with:
      risk-gate-ref: <sha>
      runs-on: '["ubuntu-latest"]'
```

`pull_request_review` has no `branches` filter, so filter in the job. The caller needs `.github/review-tiers.yml`. Optional inputs: `mode` (`shadow`), `weights-version` (`"2"`).
The job sets one `risk:tier-N` label and publishes the `risk-gate` check; on fork PRs it does neither.
Locally, `risk-gate score --weights-version N` uses the embedded `weights/vN.yml`; `--weights <path>` wins.

`baseline/2026-11-10.json` is the frozen pre-trial baseline (90 days of merged PRs).

Plan: https://chattersley.atlassian.net/wiki/spaces/FINCH/pages/2245558281
