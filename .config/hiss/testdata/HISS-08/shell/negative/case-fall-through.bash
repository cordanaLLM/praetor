#!/usr/bin/env bash
set -euo pipefail
# The fall-through terminators ;& and ;;& end a case item like ;;, so the next patterns pipe nothing into a shell.
case "$1" in
  prepare) printf '%s\n' "prepare" ;&
  */sh | */bash) printf '%s\n' "shell: $1" ;;&
  */zsh | */ksh) printf '%s\n' "other shell: $1" ;;
  quick) printf '%s\n' "quick" ;& */dash | */mksh) printf '%s\n' "small shell: $1" ;;
esac
