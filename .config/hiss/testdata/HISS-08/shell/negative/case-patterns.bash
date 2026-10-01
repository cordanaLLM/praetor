#!/usr/bin/env bash
set -euo pipefail
# The bars of a case pattern list and of a test's regular expression pipe nothing into a shell.
case "$1" in
  */sed | */sh | \
  */bash)
    printf '%s\n' "interpreter: $1"
    ;;
  *)
    printf '%s\n' "other: $1"
    ;;
esac
if [[ "$1" =~ \.(sh|bash)$ ]]; then
  printf '%s\n' "script"
fi
