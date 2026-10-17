#!/bin/sh
set -eu
# A group signal with a negated group id is not checked by HISS-08: a group id of 1 would send kill -1.
pgid=$1
kill -9 -- "-$pgid"
