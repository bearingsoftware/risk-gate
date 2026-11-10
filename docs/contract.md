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
- `attestation.attested` is self-attestation by the single maintainer. Report it as attested,
  never as an independent review.
- Unknown extra fields: ignore. Missing required fields or another `schema` value: reject and log.

## Changing the contract

Additive optional field: bump nothing, note it here. Anything else: new `schema` value
(`risk-gate.decision/v2`), the board accepts both until v1 producers are gone.
Fixtures in `testdata/decisions/` are the shared test vectors; status-board tests parse them.
