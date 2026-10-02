#!/bin/sh
# The one place the hook policy names its Python 3 interpreter (#339). Every job
# in praetor.yml and the pre-push script start their hook through this file, and
# a hook that starts another Python process reuses the interpreter it is running
# under (sys.executable), so no command spells an interpreter of its own.
#
# PRAETOR_PYTHON names the interpreter: a command on PATH or a path to it. Unset
# or empty selects python3, the name PEP 394 gives Python 3 on Linux and macOS.
# A stock Windows install has only python.exe and the py launcher, so that host
# sets the variable; scripts/dev_install.py stores it there.
set -eu
python="${PRAETOR_PYTHON:-python3}"
status=0
"$python" "$@" || status=$?
# 127: the shell found no such command. 126: it found one it cannot execute.
# Neither is a verdict of the hook, so say what is missing before returning it.
if [ "$status" -eq 127 ] || [ "$status" -eq 126 ]; then
  echo "praetor hooks: missing dependency: Python 3 interpreter '$python' did not start." \
    "Install it, or set PRAETOR_PYTHON to the interpreter the hooks run." >&2
fi
exit "$status"
