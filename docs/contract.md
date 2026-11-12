# Contract: risk-gate → status-board

Version: **decision/v1**. Plan: [FINCH-521](https://chattersley.atlassian.net/browse/FINCH-521),
consumer: [STATUS-202](https://chattersley.atlassian.net/browse/STATUS-202).

risk-gate is the producer. status-board **pulls**; nothing is pushed to the board.

## Where the decision lives

Every scoring run publishes a check run named exactly `risk-gate` on the PR head SHA.
Its `output.text` carries the decision record:

````
<!-- risk-gate:decision v1 -->
```json
{ ...decision, schema/decision.v1.json... }
```
````

- Publisher is pinned: the board accepts a `risk-gate` check run only when its `app.slug` is
  `github-actions` (risk-gate publishes with the workflow's `GITHUB_TOKEN`). Any other app or token
  with checks:write could otherwise forge a decision.
- The marker line is literal. The board parses the first ```` ```json ```` fence after it.
- Anything else in `output.text` / `output.summary` is for humans and may change freely.
- Each run also sets exactly one label `risk:tier-<det_tier>` on the PR, replacing any other `risk:tier-*`
  (convenience for humans; the check run stays the source of truth). Fork PRs get neither label nor check run
  (read-only token); the artifact is still written.
- The same JSON is uploaded as the workflow artifact `risk-decision` (debugging only; artifacts expire).
- `output.text` is capped by GitHub at 65,535 chars. The record stays under 60,000:
  `inputs.files` is capped at 300 entries and `inputs.files_truncated` says so.

## Identity and re-runs

One record per scoring run. A PR has many (every push, label, review event).

- Key: `(repo, pr, head_sha, run.id, run.attempt)`. The board upserts on it.
- The record that counts for a PR is the one with the greatest `run.at` whose `head_sha`
  equals the PR's merge head. Earlier ones are history, kept.

## Rules the board relies on

- `det_tier >= path_tier` always. A floor never goes down.
- `lane = "release"` (integration branch into main, release-please) is excluded from tier
  distribution and outcome stats. `lane = "bot"` is reported separately.
- An input risk-gate could not compute is `null` **and** named in `unavailable`. Never 0.
- `mode = "shadow"`: conclusion is always `success`; `check.would_block` says what enforce would do.
- `attestation.required` = `det_tier == 2` **and** the repo's tier file has an `attestation` block, in both
  modes. `check.would_block` = `required && !attested`, in both modes. Recorded-only repos therefore never
  show `would_block`.
- If GitHub's `changed_files` exceeds the files it returned (3000 cap), floor 2 `file_list_incomplete`.
- `attestation.attested` is self-attestation by the single maintainer. Report it as attested,
  never as an independent review.
- Unknown extra fields: ignore. Missing required fields or another `schema` value: reject and log.

## AI benchmark record (ai/v1)

Version: **ai/v1** (`schema/ai.v1.json`, fixtures in `testdata/ai/`). A shadow benchmark of a model's
review tier next to the deterministic one. It is never used by the gate.

- Separate check run named exactly `risk-gate-ai`, conclusion `neutral`, on the PR head SHA. Title and
  summary carry no tier. `output.text` is the marker line `<!-- risk-gate:ai v1 -->` then the first
  ```` ```json ```` fence, the same shape as the decision record.
- Publisher is pinned: accept only a check run whose `app.slug` is `github-actions`.
- Key: `(repo, pr, head_sha)`, first classified wins. The job skips the call when a `classified` record by
  `github-actions` already exists on the head SHA, so a re-run or label event adds nothing. Several
  `skipped` rows may precede it; take the first `classified`, else the latest skip.
- A skip is a row, not an absence: `status = "skipped"` with `skip_reason` one of `no_key`,
  `not_human_lane`, `fork`, `refusal`, `truncated`, `parse_error`, `credit_or_limit`, `api_error`.
  `api_error` also covers a failed GitHub read of the PR or its files during classify.
  `ai`, `usage`, `cost_usd`, `latency_ms` and `model_used` are `null` when there was nothing to record.
  Fork PRs write no check run (read-only token); the `risk-ai` artifact is still written.
- Human lane only. `det_tier`, `weights_version` and `lane` are copied from the decision record.
  `prompt_version` is the `prompts/classifier/<version>/` directory; `prompt_sha256` covers its
  `system.md` and `schema.json`.
- Model text is sanitised before it is stored (no control characters, backticks become `'`) and the
  JSON is HTML-escaped, so a reason cannot close the fence or inject markup.
- Consumers are the dataset and the benchmark view only. Nothing here feeds a tier, label or check
  conclusion. Unknown extra fields: ignore.
- The job never writes to the PR (no comments, labels or `pull-requests: write`). The record is also
  uploaded as the workflow artifact `risk-ai` (14 days).

## Evidence (optional `evidence` block)

An additive optional field of decision/v1 (nothing bumped; FINCH-549). It is present only when the repo's tier
file has an `evidence` block; without one the record is byte-identical to before. Fields:
`source` (`compute` | `artifact`), `applied`, `status` (`ok` | `skipped`), `skip_reason` (`null` or
`not_configured`, `compute_failed`, `artifact_missing`, `artifact_timeout`, `parse_error`), `coverage_base`,
`coverage_head`, `api_break`, `generated_by`, `evidence_sha256`, and what the decision would have been had the
evidence been applied: `would_score`, `would_det_tier`, `would_floors`. All but the first four are `null` on a skip.

- During the trial `applied` is `false`: the evidence is recorded only. `score`, `floors` and `det_tier` are
  exactly what they would be without it, and `inputs.coverage_delta` / `inputs.api_break` stay `null` and in
  `unavailable`. Compare `would_*` with the real values to see what enforcing would change.
- When `applied` is `true` the evidence feeds `inputs.coverage_delta` and `inputs.api_break` through the scorer's
  usual path, and `would_*` equals the record's own `score`, `floors` and `det_tier`.
- A skip is a row, never a failure and never a 0.
- `evidence_sha256` covers the `evidence.json` file written by `ctf evidence`.
- Unknown extra fields: ignore, as everywhere else. `schema/decision.v1.json` sets no `additionalProperties`
  restriction, so a consumer that has not learned the block still accepts the record.

## Reusable workflow

Consumers call `.github/workflows/risk-gate.yml` (`on: workflow_call`). Inputs: `runs-on` (JSON array of
labels), `mode` (default `shadow`), `weights-version` (default `"2"`, embedded in the binary), `risk-gate-ref`
(the commit SHA the caller pinned `uses:` to; the binary is installed from it). Optional secret
`anthropic-api-key` enables the AI benchmark; without it the `classify` job records a `no_key` skip. The `score`
job also requests `packages: read` and `actions: read` for the evidence block; callers must grant both. See the README.

## Changing the contract

Additive optional field: bump nothing, note it here. Anything else: new `schema` value
(`risk-gate.decision/v2`), the board accepts both until v1 producers are gone.
Fixtures in `testdata/decisions/` are the shared test vectors; status-board tests parse them.
