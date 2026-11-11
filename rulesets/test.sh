#!/usr/bin/env bash
# Offline checks: ruleset bodies and apply.sh argument validation. No network.
set -euo pipefail

DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
fails=0

check() {
  local name=$1
  shift
  if "$@" >/dev/null 2>&1; then
    echo "ok   $name"
  else
    echo "FAIL $name"
    fails=$((fails + 1))
  fi
}

# repo, integration branch
cases=(
  "secure-repo develop"
  "joule-genie develop"
  "component-test-framework main"
)

for c in "${cases[@]}"; do
  read -r repo branch <<<"$c"
  f="$DIR/$repo.json"
  check "$repo: parses as JSON" jq -e . "$f"
  check "$repo: name is trial-integration" jq -e '.name == "trial-integration"' "$f"
  check "$repo: target is branch" jq -e '.target == "branch"' "$f"
  check "$repo: enforcement is the placeholder" jq -e '.enforcement == "__ENFORCEMENT__"' "$f"
  # shellcheck disable=SC2016 # $b is a jq variable, bound by --arg
  check "$repo: targets exactly refs/heads/$branch" \
    jq -e --arg b "refs/heads/$branch" '.conditions.ref_name.include == [$b] and .conditions.ref_name.exclude == []' "$f"
  check "$repo: no bypass actors" jq -e '.bypass_actors == []' "$f"
  check "$repo: no pull_request rule" jq -e '[.rules[].type] | index("pull_request") | not' "$f"
  check "$repo: no reviewer or approval fields anywhere" \
    jq -e '[.. | objects | keys[] | select(test("review|approv|code_owner|reviewer"; "i"))] | length == 0' "$f"
  check "$repo: has required_status_checks with checks" \
    jq -e '[.rules[] | select(.type == "required_status_checks") | .parameters.required_status_checks | length] | add > 0' "$f"
  check "$repo: every required check pinned to GitHub Actions app 15368" \
    jq -e '[.rules[] | select(.type == "required_status_checks") | .parameters.required_status_checks[] | .integration_id] | all(. == 15368)' "$f"
done

check "apply.sh: rejects unknown repo" bash -c \
  "! '$DIR/apply.sh' no-such-repo"
check "apply.sh: rejects path traversal in repo name" bash -c \
  "! '$DIR/apply.sh' ../secure-repo"
check "apply.sh: rejects unknown enforcement" bash -c \
  "! '$DIR/apply.sh' --enforcement bogus secure-repo"
check "apply.sh: rejects no repo" bash -c \
  "! '$DIR/apply.sh'"
check "apply.sh: rejects unknown flag" bash -c \
  "! '$DIR/apply.sh' --force secure-repo"

if command -v shellcheck >/dev/null 2>&1; then
  check "shellcheck apply.sh" shellcheck "$DIR/apply.sh"
  check "shellcheck test.sh" shellcheck "$DIR/test.sh"
else
  echo "skip shellcheck (not installed)"
fi

if ((fails > 0)); then
  echo "$fails check(s) failed"
  exit 1
fi
echo "all checks passed"
