#!/bin/sh
set -eu
# "|| true" discards the command's failure.
rm -r cache || true
