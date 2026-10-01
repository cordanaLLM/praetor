#!/bin/sh
set -eu
# A function calling another function is delegation, not recursion.
build() {
  printf '%s\n' "building $1"
}
release() {
  build "$1"
}
release app
