#!/usr/bin/env bash
set -o errexit -o nounset -o pipefail
# The long option names enable the same strict mode.
sort input.txt | uniq > output.txt
