#!/usr/bin/env bash
# Usage: rulesets/apply.sh [--apply] [--enforcement evaluate|active] <repo>...
# Default is a dry run: prints the diff between the desired ruleset and the live one.
set -euo pipefail

ORG=bearingsoftware
RULESET_NAME=trial-integration
DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)

usage() {
  sed -n '2,3p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'
}

apply=false
enforcement=evaluate
repos=()
while (($#)); do
  case "$1" in
    --apply) apply=true; shift ;;
    --enforcement) enforcement=${2:?--enforcement needs a value}; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    -*) echo "unknown flag: $1" >&2; usage >&2; exit 2 ;;
    *) repos+=("$1"); shift ;;
  esac
done

case "$enforcement" in
  evaluate|active) ;;
  *) echo "invalid enforcement: $enforcement (want evaluate or active)" >&2; exit 2 ;;
esac

if ((${#repos[@]} == 0)); then
  usage >&2
  exit 2
fi

# Validate every repo before any API call.
for repo in "${repos[@]}"; do
  if [[ ! $repo =~ ^[a-z0-9-]+$ || ! -f "$DIR/$repo.json" ]]; then
    echo "unknown repo: $repo (no rulesets/$repo.json)" >&2
    exit 2
  fi
done

# Keep only the fields that define the ruleset; drop server-set ids, timestamps and links.
normalise() {
  jq -S '{
    name, target, enforcement,
    conditions: {ref_name: {
      include: (.conditions.ref_name.include // [] | sort),
      exclude: (.conditions.ref_name.exclude // [] | sort)
    }},
    bypass_actors: (.bypass_actors // []),
    rules: (.rules
      | map({type, parameters: (.parameters // {})})
      | map(if .type == "required_status_checks"
            then .parameters.required_status_checks |= sort_by(.context)
            else . end)
      | sort_by(.type))
  }'
}

for repo in "${repos[@]}"; do
  echo "== $ORG/$repo"
  desired=$(jq --arg e "$enforcement" '.enforcement = $e' "$DIR/$repo.json")

  ids=$(gh api --paginate "repos/$ORG/$repo/rulesets" \
    --jq ".[] | select(.name == \"$RULESET_NAME\") | .id")
  if [[ $ids == *$'\n'* ]]; then
    echo "more than one ruleset named $RULESET_NAME in $ORG/$repo; resolve by hand" >&2
    exit 1
  fi

  have='{}'
  if [[ -n $ids ]]; then
    have=$(gh api "repos/$ORG/$repo/rulesets/$ids" | normalise)
  else
    echo "no existing ruleset named $RULESET_NAME: will create"
  fi
  want=$(printf '%s' "$desired" | normalise)

  diff_out=$(diff <(printf '%s\n' "$have") <(printf '%s\n' "$want") || true)
  if [[ -z $diff_out ]]; then
    echo "unchanged"
    continue
  fi
  printf '%s\n' "$diff_out"

  if [[ $apply == false ]]; then
    echo "dry run: nothing written (pass --apply to write)"
    continue
  fi

  if [[ -z $ids ]]; then
    printf '%s' "$desired" | gh api -X POST "repos/$ORG/$repo/rulesets" --input - >/dev/null
    echo "created"
  else
    printf '%s' "$desired" | gh api -X PUT "repos/$ORG/$repo/rulesets/$ids" --input - >/dev/null
    echo "updated ruleset $ids"
  fi
done
