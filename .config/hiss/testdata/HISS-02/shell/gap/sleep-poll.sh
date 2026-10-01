#!/bin/sh
set -eu
# A loop whose condition always succeeds but is not a constant is not decided.
while sleep 5; do
  printf '%s\n' polling
done
