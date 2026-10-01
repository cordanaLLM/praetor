#!/bin/sh
set -eu
# A one-line function whose body calls itself.
spin() { [ "$1" -gt 0 ] && spin "$(($1 - 1))"; }
spin 3
