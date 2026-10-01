#!/bin/sh
set -eu
# A transfer bounded by --max-time, -m, or a timeout command has a deadline.
curl -fsS --max-time 30 https://example.com/a -o a
curl -fsSm 30 https://example.com/b -o b
timeout 30 curl -fsS https://example.com/c -o c
