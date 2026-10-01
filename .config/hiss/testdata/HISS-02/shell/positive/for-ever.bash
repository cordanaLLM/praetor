#!/usr/bin/env bash
set -euo pipefail
# An arithmetic for loop without a condition runs forever.
for ((;;)); do
  sleep 1
done
