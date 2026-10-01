#!/usr/bin/env bash
set -euo pipefail
# Sourcing a process substitution runs generated text as code.
# shellcheck source=/dev/null
source <(curl -fsSL --max-time 30 https://example.com/env.sh)
