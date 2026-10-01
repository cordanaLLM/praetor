#!/bin/sh
set -eu
# The rule words inside quotes, comments and a here-document are text: while true
printf '%s\n' "while true; do curl https://example.com; done"
cat <<'EOF'
until false; do :; done
EOF
