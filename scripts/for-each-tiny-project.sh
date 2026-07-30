#!/usr/bin/env sh

set -xeu

if [ "$#" -eq 0 ]; then
  echo "usage: $0 COMMAND [ARG...]" >&2
  exit 2
fi

for project_dir in tiny_*/; do
  project_dir=${project_dir%/}

  if [ ! -f "$project_dir/go.mod" ]; then
    continue
  fi

  printf '\n==> %s\n' "$project_dir"

  (
    cd "$project_dir"
    "$@"
  )
done
