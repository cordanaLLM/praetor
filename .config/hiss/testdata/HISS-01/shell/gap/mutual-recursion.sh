#!/bin/sh
set -eu
# Mutual recursion needs a call graph across functions; only a direct self-call is decided.
ping_side() {
  if [ "$1" -gt 0 ]; then
    pong_side "$(($1 - 1))"
  fi
}
pong_side() {
  if [ "$1" -gt 0 ]; then
    ping_side "$(($1 - 1))"
  fi
}
ping_side 4
