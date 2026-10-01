#!/bin/sh
set -eu
# A command string handed to a child shell is evaluated too, but sh -c is not decided.
sh -c "$1"
