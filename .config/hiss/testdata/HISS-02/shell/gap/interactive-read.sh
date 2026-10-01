#!/bin/sh
set -eu
# read without -t waits on a terminal forever; the I/O-timeout half is not decided for read.
printf '%s' "Continue? "
read -r answer
printf '%s\n' "$answer"
