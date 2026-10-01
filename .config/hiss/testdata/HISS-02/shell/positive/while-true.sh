#!/bin/sh
set -eu
# A while true loop carries no scalar upper bound.
while true; do
  printf '%s\n' waiting
  sleep 1
done
