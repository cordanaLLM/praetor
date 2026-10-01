#!/bin/sh
set -eu
# A counted loop has a scalar bound.
attempt=0
while [ "$attempt" -lt 5 ]; do
  attempt=$((attempt + 1))
done
