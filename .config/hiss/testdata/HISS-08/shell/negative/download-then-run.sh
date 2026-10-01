#!/bin/sh
set -eu
# A downloaded script that is checked and then run as a file is not text piped into a shell.
curl -fsSL --max-time 30 https://example.com/install.sh -o install.sh
sha256sum -c install.sh.sha256
sh install.sh
