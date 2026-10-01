#!/bin/sh
set -eu
# Turning errexit off around a command without reading its status is not decided.
set +e
rm -r cache
set -e
