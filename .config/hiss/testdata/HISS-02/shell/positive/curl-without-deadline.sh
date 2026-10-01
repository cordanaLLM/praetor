#!/bin/sh
set -eu
# curl without --max-time can wait on a stalled transfer without any deadline.
curl -fsSL https://example.com/archive.tar.gz -o archive.tar.gz
