# `.github/review-tiers.yml` — format v1

Committed in each trial repo, read by risk-gate **from the base branch**. Itself a tier 2 path.

```yaml
version: 1
owner: "@chattersley"
default_tier: 1
tiers:
  2:
    description: Sensitive. Recorded as needing two approvals including a code owner.
    paths:                       # gitignore-style globs, ** allowed, matched against repo-relative paths
      - "backend/internal/auth/**"
      - ".github/workflows/**"
      - ".github/review-tiers.yml"
      - ".github/CODEOWNERS"
  0:
    description: Docs and generated code. Merges on green checks.
    paths:
      - "**/*.md"
      - "docs/**"
    bot_authors: ["renovate[bot]"]
    bot_conditions:
      update_type: patch         # parsed from the Renovate PR body's Change column; unknown => not tier 0
      require_provenance: false  # no provenance check exists here; recorded, not verified
release:
  heads: ["develop", "release-please--*"]   # head refs whose PRs are lane=release
attestation:                     # only read in mode=enforce
  label: tier2-self-reviewed
  permitted: ["chattersley"]
```

Rules:

- A file not matched by tier 2 or tier 0 is `default_tier`. Tier 2 wins over tier 0 when both match.
- A PR's `path_tier` is the max over its files. A renamed file counts at the higher of its old
  and new path (moving code out of `auth/` is still an auth change).
- Bot tier 0 needs: author in `bot_authors`, every file tier 0 **or** a dependency manifest/lockfile,
  `bot_update_type` equal to `update_type`, and score 0. Human-written changes are never tier 0
  unless every file is a tier 0 path and score is 0–1 (as the plan's mapping says).
- `.github/review-tiers.yml`, `.github/CODEOWNERS` and `.github/workflows/**` must be tier 2;
  risk-gate adds them as `control_file` floors even if the file forgets them.
- CODEOWNERS must list every tier 2 glob. `risk-gate lint-tiers` fails when they differ.
