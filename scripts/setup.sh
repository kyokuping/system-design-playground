#!/usr/bin/env sh

set -xeu

if ! command -v lefthook >/dev/null 2>&1; then
  echo "lefthook is unavailable; enter the Nix development shell first." >&2
  exit 1
fi

lefthook install
