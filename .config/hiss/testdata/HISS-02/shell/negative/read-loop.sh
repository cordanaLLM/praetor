#!/bin/sh
set -eu
# A loop over a file's lines ends with its input.
while IFS= read -r line; do
  printf '%s\n' "$line"
done < "$1"
