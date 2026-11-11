# Trial integration rulesets

Repository rulesets for the three trial repos, kept as code. One body per repo in
`<repo>.json`, applied by `apply.sh`, checked offline by `test.sh`.

| Repo | Ruleset target | Required checks (integration app 15368, GitHub Actions) |
|---|---|---|
| `secure-repo` | `develop` | `action-paths`, `component-api`, `component-cli`, `component-mcp`, `component-web`, `docs`, `e2e`, `frontend`, `lint`, `mutation-pilot`, `op-map-spec`, `security`, `setup-action`, `test`, `title` |
| `joule-genie` | `develop` | `ci-success` |
| `component-test-framework` | `main` | `build`, `lint`, `parallel-reference`, `playwright-test-example`, `risk-gate`, `unit-test` |

## What is enforced

- `deletion`: the integration branch cannot be deleted.
- `non_fast_forward`: no force-push to the integration branch.
- `required_status_checks`: each listed check must pass on the PR head before GitHub merges.
  `strict_required_status_checks_policy` is `false`, so a PR does not have to be up to date
  with the base before merge.
- No bypass actors, so the maintainer cannot merge around a red check.

Enforcement is `evaluate` by default: GitHub records what would have been blocked without
blocking it. `--enforcement active` turns the rules on.

## Why no approvals

The repos have a single maintainer. A `pull_request` rule with required approvals, required
reviewers or code-owner review would wait for a second approver that does not exist, so every
PR would deadlock. The rules gate on automated checks only.

## Check selection

- **secure-repo** has no aggregate job. Its `ci.yml` has no path filter and no PR-only `if:`,
  so all 14 of its jobs run on every PR into `develop`. Those plus `title` (from `pr-title.yml`)
  appeared on the PR head commit of each of the last five merged PRs: #1291, #1292, #1293,
  #1294, #1295. Excluded: `actionlint` (only runs when `.github/**` changes; seen on #1291 and
  #1292 only) and `prune` (housekeeping job from `prune-actions-storage.yml`, not a gate).
- **joule-genie** `ci-success` is the aggregate (`needs:` the gated jobs, verdict script at
  `scripts/ci/ci-success-verdict.py`). It passed on the PR head of #1883, #1884 and #1885.
  Jobs skipped by path filters pass through it.
- **component-test-framework** has no aggregate. All five jobs ran on the PR head of each of
  #69 to #73, all green, on `ubuntu-latest`.

## Known gap: joule-genie PRs that path-filter out `ci.yml`

`ci.yml` has a `paths-ignore` for `android/**`, `corporate-site/**` and a few E2E and docs
paths. A PR that touches only those files never starts `ci.yml`, so `ci-success` is never
reported. A required check that never reports leaves the PR blocked, as Renovate's own gate
already does for the same case (see the renovate-config README, "What it cannot see").

Evidence on develop:

- #1880 (android-ci.yml only) and #1879 (corporate-site only) merged with no `ci-success`
  check run on the PR head.

This must be settled before `joule-genie` moves to `active`. Options, for the orchestrator to
choose: drop the `paths-ignore` entries that exclude sibling-workflow-only PRs, or add a
required check that every PR always reports. This ruleset is not safe to make `active`
until one of those lands.

## risk-gate as a required check (FINCH-529)

Only `component-test-framework` requires `risk-gate`, and only from trial week 3 (1 Dec 2026; timeline halved 11 Nov),
together with `mode: enforce` in its caller (component-test-framework#77). `secure-repo` and
`joule-genie` are recorded-only: they never require it.

In enforce mode the `risk-gate` check fails on a human-lane PR whose deterministic tier is 2,
until the attestation label is on the PR. Tiers 0 and 1 and the bot lanes pass as before.

### Releasing a held PR (normal path)

1. Read the `risk-gate` check summary: it names the tier, the floors and the top score items.
2. Review the change yourself, then add `tier2-self-reviewed`. Only users listed under
   `attestation.permitted` in `.github/review-tiers.yml` count; a label from anyone else
   leaves the PR held.
3. The `labeled` event re-runs risk-gate; the check goes green and the record says
   `attested: true`. The board reports it as self-attested, never as independent review.

Use the same label when you think the tier is wrong. The record keeps the score, so the
benchmark can still count it as a disagreement.

### When risk-gate itself cannot report (emergency path)

The ruleset has no bypass actors, so nobody can merge past a missing or failed `risk-gate`
check. Cases: the scorer job fails or times out, GitHub Actions is down, or a fork PR (risk-gate
publishes nothing on forks, so the required check never reports).

- Fork PR: push the contributor's branch to the repo and open the PR from there. Do not use
  the emergency path for it.
- Otherwise, drop the ruleset to evaluate, merge, and turn it back on straight away:

  ```sh
  rulesets/apply.sh --apply --enforcement evaluate component-test-framework
  gh pr merge <N> -R bearingsoftware/component-test-framework --merge
  rulesets/apply.sh --apply --enforcement active component-test-framework
  ```

  Comment on FINCH-529 with the PR number and why. Each use counts against the trial.

### Switch-on (1 Dec 2026)

1. Merge this repo's change that adds `risk-gate` to `component-test-framework.json`.
2. Mark component-test-framework#77 ready, merge it once green.
3. `rulesets/apply.sh --apply --enforcement active component-test-framework`, then the dry
   run again: it must print `unchanged`.
4. Open a deliberately risky test PR (a tier 2 floor path, e.g. the workflow directory) and
   confirm it is held; add the label and confirm it is released. Close the test PR.

## Code scanning: not included

`code_scanning` needs a repo that already uploads results. Advanced Security is not enabled on
`secure-repo` or `joule-genie` (HTTP 403 from `code-scanning/analyses`). `component-test-framework`
returns no analysis (HTTP 404). The rule is left out of all three bodies.

## Renovate

- Base branches: `secure-repo` and `joule-genie` set `baseBranchPatterns: ["develop"]`.
  `component-test-framework` has no override, so Renovate targets `main`.
- Shared preset (`bearingsoftware/renovate-config` `default.json`): `platformAutomerge: false`,
  `automergeStrategy: "merge-commit"`, `automerge: false` at the top level, patch/minor/pin/digest
  automerge on. Renovate merges the PR itself once its own verdict is green. The repo setting
  `allow_auto_merge` is not used.
- Lock file maintenance: automerged in `secure-repo`. Not automerged in `joule-genie`.
- Renovate PRs run the same checks as human PRs. Evidence: `secure-repo` #1295 (lock file
  maintenance) ran all 15 required checks; `component-test-framework` #69 to #72 (Renovate)
  ran all 5.
- Interaction: the required checks are the GitHub-side backstop to Renovate's gate. If a
  required check is missing or failed, GitHub refuses the merge that Renovate also would not
  make. A check that never starts leaves the PR pending on both sides.

## How to apply

Dry run (default) prints the diff between the desired ruleset and the live one. It only reads.

```sh
rulesets/apply.sh secure-repo joule-genie component-test-framework
rulesets/apply.sh --enforcement active component-test-framework
```

`--apply` creates the ruleset when none is named `trial-integration`, and otherwise PUTs it by
id. A second `--apply` with nothing changed prints `unchanged` and writes nothing.

```sh
rulesets/apply.sh --apply --enforcement evaluate secure-repo
```

Check the offline tests and shellcheck before applying:

```sh
rulesets/test.sh
```

Known limit: the diff normalises the fields it knows about (name, target, enforcement, ref
conditions, bypass actors, rules and check contexts, sorted). If GitHub adds a server default to
a rule's parameters, the diff will show it on every run. Re-run the dry run right after the first
apply to confirm it prints `unchanged`.
