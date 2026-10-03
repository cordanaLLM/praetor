#!/bin/sh
set -eu
exec sh .config/lefthook/python.sh .config/lefthook/scripts/hooks.py pre-push "${1-}"
