#!/bin/sh
set -eu
# The word eval in text and a function named evaluate are not dynamic evaluation.
evaluate() {
  printf '%s\n' "eval is banned here"
}
evaluate
