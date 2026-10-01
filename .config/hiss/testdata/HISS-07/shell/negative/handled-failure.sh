#!/bin/sh
set -eu
# A failure handled by a command that reports it is not discarded.
report() {
  printf '%s\n' "failed: $1" >&2
}
rm -r cache || report cache
