#!/bin/bash
set -eu
# Under Bash a pipeline's status is its last command's; without pipefail a failure on the left is lost.
sort input.txt | uniq > output.txt
