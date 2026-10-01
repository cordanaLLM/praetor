#!/bin/sh
set -eu
# A function that runs its own name inside its body recurses without a bound.
countdown() {
  if [ "$1" -gt 0 ]; then
    countdown "$(($1 - 1))"
  fi
}
countdown 3
