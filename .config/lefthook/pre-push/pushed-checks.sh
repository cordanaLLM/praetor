#!/bin/sh
exec python3 .config/lefthook/scripts/hooks.py pre-push "$1"
