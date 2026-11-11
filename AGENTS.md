# risk-gate — agent & contributor guide

Follows [`bearingsoftware/code-standards`](https://github.com/bearingsoftware/code-standards)
(`standards/_cross-cutting.md`, `standards/go.md`). Jira: **FINCH** (epic FINCH-521).

- Go, stdlib first. `go test ./...` must pass with no network: GitHub is behind an interface, faked in tests.
- `docs/contract.md` and `schema/decision.v1.json` are consumed by status-board. Changing them
  needs the orchestrator's sign-off; never change a fixture to make a test pass.
- Tier 2 paths here: `weights/**`, `schema/**`, `prompts/**`, `.github/workflows/**`.
- Weights are not tuned during the trial (from shadow start, 11 Nov 2026; the timeline was halved, so the freeze moved up from 24 Nov).
