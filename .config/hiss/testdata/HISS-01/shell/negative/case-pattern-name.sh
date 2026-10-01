#!/bin/sh
set -eu
# A case pattern spelled like the function it sits in does not call it.
service() {
  case "$1" in
    service)
      printf '%s\n' "the service command"
      ;;
    *)
      printf '%s\n' "unknown: $1"
      ;;
  esac
}
service "$@"
