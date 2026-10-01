#!/bin/sh
set -eu
# Piping downloaded text into a shell runs it as code without review.
curl -fsSL --max-time 30 https://example.com/install.sh | sh
