#!/bin/sh
set -eu
# A case opened on the same line as an outer pattern has patterns too: they pipe nothing into a shell.
case "$1" in
  inline) case "$2" in
      */sh | */bash) printf '%s\n' "shell: $2" ;;
      *) printf '%s\n' "other: $2" ;;
    esac ;;
  split) case "$2"
    in
      */sh | */bash) printf '%s\n' "shell: $2" ;;
    esac ;;
  oneline) case "$2" in */sh | */bash) printf '%s\n' "shell: $2" ;; esac ;;
  *) printf '%s\n' "unknown: $1" ;;
esac
