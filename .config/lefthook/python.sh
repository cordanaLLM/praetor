#!/bin/sh
# The one place the hook policy names its Python 3 interpreter (#339). Every job
# in praetor.yml and the pre-push script start their hook through this file, and
# a hook that starts another Python process reuses the interpreter it is running
# under (sys.executable), so no command spells an interpreter of its own.
#
# PRAETOR_PYTHON names the interpreter: a command on PATH or a path to it. Unset
# or empty selects python3, the name PEP 394 gives Python 3 on Linux and macOS.
# A python.org install on Windows has python.exe and the py launcher, and the
# python3 that Windows itself puts on PATH is the Microsoft Store alias, not an
# interpreter. That host sets the variable; scripts/dev_install.py stores it.
set -eu
python="${PRAETOR_PYTHON:-python3}"
status=0
"$python" "$@" || status=$?
# A nonzero status is the hook's verdict only when the interpreter ran. The shell
# returns 127 for no such command and 126 for one it cannot execute, and the
# Microsoft Store alias named python3 starts, prints "Python was not found" and
# returns a status of its own. So the status is not read: on a failure the
# interpreter is started once more with an empty program, and one that cannot
# run even that is reported as the missing dependency it is.
if [ "$status" -ne 0 ] && ! "$python" -c "" </dev/null >/dev/null 2>&1; then
  echo "praetor hooks: missing dependency: Python 3 interpreter '$python' did not start." \
    "Install it, or set PRAETOR_PYTHON to the interpreter the hooks run." >&2
fi
exit "$status"
