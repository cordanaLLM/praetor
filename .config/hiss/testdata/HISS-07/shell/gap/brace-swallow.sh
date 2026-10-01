#!/bin/sh
set -eu
# A failure discarded through a brace group is not decided; only "|| true" and "|| :" are.
rm -r cache || { :; }
