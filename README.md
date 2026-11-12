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
    secrets:
      anthropic-api-key: ${{ secrets.RISK_GATE_ANTHROPIC_API_KEY }}
```

`pull_request_review` has no `branches` filter, so filter in the job. The caller needs `.github/review-tiers.yml`. Optional inputs: `mode` (`shadow`), `weights-version` (`"2"`).
The job sets one `risk:tier-N` label and publishes the `risk-gate` check; on fork PRs it does neither.
A second job, `classify`, records a shadow AI benchmark as the `risk-gate-ai` check run (`risk-gate classify`, see `docs/contract.md`); it needs `checks: write` only and never touches the PR.
Locally, `risk-gate score --weights-version N` uses the embedded `weights/vN.yml`; `--weights <path>` wins.

## Test evidence (optional, shadow by default)

A repo can have risk-gate record test evidence from `@bearingsoftware/component-test-framework` 0.3.16+
(`ctf evidence`: status coverage before and after, and whether the API spec breaks clients) by adding an
`evidence` block to `.github/review-tiers.yml` ([format](schema/review-tiers.md)). Absent, nothing changes and the
decision is byte-identical to before. Two sources:

- `compute`: the score job runs the pinned `ctf evidence` itself, over the base and head SHAs, in a separate
  `src/` checkout. It installs no PR dependency and runs no PR code (`ctf evidence` reads git objects). The
  package is installed from GitHub Packages with the job token, with `--ignore-scripts`, into a private prefix; `ctf`
  then runs in a separate step with no token and the token `.npmrc` already deleted. The package's transitive
  dependencies resolve from public npm at run time (no lockfile yet; pinning them is a follow-up).
  Evidence files are size-capped (1 KB for the text files, 1 MB for `evidence.json`) and `generatedBy` must look like
  `ctf evidence <version>`; anything else is a `parse_error` skip.
- `artifact`: the score job waits (polling every 30 s, up to `wait_seconds`, default 600, max 1800) for the
  consumer's `workflow` run on the head SHA to finish, then downloads its `ctf-evidence` artifact. That artifact
  is produced by the PR's own CI, so a PR can influence it; prefer `compute` once evidence is applied.

By default (`apply: false`) the evidence is only **recorded**: the decision gains an `evidence` block with the values
and what the score, floors and tier *would* be if applied (`would_*`), while `score`, `floors`, `det_tier`,
`inputs.coverage_delta` and `inputs.api_break` are exactly as without it. `apply: true` feeds the coverage delta and
`api_break` into the scorer, with the existing weights. Nothing in the trial sets it. Evidence that cannot be
produced or read is recorded as a skip (`compute_failed`, `artifact_missing`, `artifact_timeout`, `parse_error`);
it never fails the gate and is never read as 0. See [contract](docs/contract.md#evidence-optional-evidence-block).

Permissions. The score job requests `packages: read` (compute) and `actions: read` (artifact). GitHub documents that
called workflows can only downgrade permissions, never raise them. Observed behaviour: when a called job asks for more
than the calling job grants, the run fails at startup with a permissions error and nothing is scored. So when bumping the
pinned SHA to a version with evidence, the calling job must become:

```yaml
    permissions: {contents: read, pull-requests: write, checks: write, packages: read, actions: read}
```

Once both are granted, a source that needs a permission the token still lacks (for example a `packages: read` that
the org's package settings do not extend to this repo) fails its step; that is recorded as a skip and the gate
carries on.

`baseline/2026-11-10.json` is the frozen pre-trial baseline (90 days of merged PRs).

Plan: https://chattersley.atlassian.net/wiki/spaces/FINCH/pages/2245558281
