#!/usr/bin/env bash

set -euo pipefail

if (( $# != 3 )); then
  echo "usage: $0 BEFORE_SHA HEAD_SHA DEFAULT_BRANCH_REF" >&2
  exit 2
fi

before_sha=$1
head_sha=$2
default_branch_ref=$3
base=$before_sha
all_modules=false

if [[ "$base" =~ ^0+$ ]] ||
  ! git cat-file -e "$base^{commit}" 2>/dev/null ||
  ! git merge-base --is-ancestor "$base" "$head_sha"; then
  base="$(git merge-base "$default_branch_ref" "$head_sha")"
  [[ "$base" == "$head_sha" ]] && all_modules=true
fi

if $all_modules; then
  modules="$(
    git ls-files |
      awk -F/ 'NF == 2 && $1 ~ /^tiny_/ && $2 == "go.mod" { print $1 }' |
      sort -u
  )"
else
  modules="$(
    git diff --name-only "$base" "$head_sha" |
      awk -F/ '
        $1 ~ /^tiny_/ &&
        ($NF ~ /\.go$/ || $NF == "go.mod" || $NF == "go.sum") {
          print $1
        }
      ' |
      sort -u |
      while IFS= read -r module; do
        [[ -f "$module/go.mod" ]] && printf '%s\n' "$module"
      done
  )"
fi

if [[ -z "$modules" ]]; then
  echo "No changed tiny_* Go modules found" >&2
  exit 1
fi

awk 'BEGIN { printf "[" } { if (NR > 1) printf ","; printf "\"%s\"", $0 } END { print "]" }' \
  <<< "$modules"
