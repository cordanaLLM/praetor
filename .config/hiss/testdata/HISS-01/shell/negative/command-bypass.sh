#!/bin/sh
set -eu
# "command download" runs the program named download and never the function, so this wrapper does not recurse.
download() {
  command download "$@"
}
download https://example.com
