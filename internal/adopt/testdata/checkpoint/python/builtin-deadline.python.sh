#!/bin/sh
# The one place the hook policy starts its Python 3 interpreter (#339). Every job
# in praetor.yml and the pre-push script start their hook through this file, and
# a hook that starts another Python process reuses the interpreter it is running
# under (sys.executable), so no command spells an interpreter of its own.
#
# No variable selects the program: a value naming anything that exits 0 would
# pass every gate without running it. The candidates are a fixed list, tried in
# order, the same one the operator setting hooks.python defaults to
# (internal/config/operator_sections.go) and scripts/toolchain.py declares.
#
# Try the checkout's untracked virtualenv first, then the main worktree's.
# A tracked virtualenv can contain a fake interpreter and is never trusted.
#
# A candidate is trusted only once it has proven itself: it runs the probe and
# must state Python 3 at or above the floor. A name on PATH proves nothing.
# Windows puts python.exe and python3.exe there as Microsoft Store aliases,
# which start, print "Python was not found" and exit nonzero. The first proven
# candidate replaces this shell, so its status is the hook's. With none left
# the hook fails as a missing dependency and names what was tried.
#
# Every line below ends in a comment sign, blank ones included. praetorctl adopt
# writes this file into repositories whose checkout may give it CRLF line
# endings (core.autocrlf), and a shell reads the carriage return as part of the
# last word of a line. Behind a comment sign it is part of the comment.
set -eu #
# Python 3.10: the release .config/hook-lint/requirements.txt is compiled for.
floor=10                                                                         #
probe='import sys; print(sys.version_info[0], sys.version_info[1], sep=chr(46))' #
# Python on Windows ends the line it prints with a carriage return.
cr=$(printf '\r')                  #
tried=''                           #
resolve=false                      #
if [ "${1-}" = "--resolve" ]; then #
  resolve=true                     #
  shift                            #
fi                                 #
#
# Drop Git repository selection so nested git invocations (e.g. in test jobs)
# cannot target the pushing repository. Pre-commit hooks pass --keep-index to
# preserve GIT_INDEX_FILE for partial-commit staging.
keep_index=false                                                                                 #
if [ "${1-}" = "--keep-index" ]; then                                                            #
  keep_index=true                                                                                #
  shift                                                                                          #
fi                                                                                               #
unset GIT_DIR GIT_WORK_TREE GIT_COMMON_DIR GIT_OBJECT_DIRECTORY GIT_ALTERNATE_OBJECT_DIRECTORIES #
if [ "$keep_index" != true ]; then                                                               #
  unset GIT_INDEX_FILE                                                                           #
fi                                                                                               #
#
# Bash monitor mode gives the probe its own process group. A builtin timed read
# owns the deadline: a separately scheduled sleep cannot postpone it. Disable
# startup-file and imported-function processing with -p. Only the query is timed.
bounded_probe() ( #
  status=0 #
  # Trust caller PATH for Bash (e.g. Nix); -p lookup misses nonstandard installs. #
  # Unlike a default-path lookup, this permits a caller-supplied Bash executable. #
  probe_bash=$(command -v bash) || exit 125 #
  # The literal program expands in child Bash, after enabling its job control. #
  # shellcheck disable=SC2016 #
  probe_script=' #
    set -m #
    probe=$1; shift #
    probe_dir=$(command -p mktemp -d 2>/dev/null) || exit 125 #
    probe_pid="" #
    cleanup() { #
      if [ -n "$probe_pid" ]; then #
        kill -KILL -- "-$probe_pid" 2>/dev/null || : #
        wait "$probe_pid" 2>/dev/null || : #
      fi #
      command -p rm -rf "$probe_dir" #
    } #
    trap cleanup EXIT #
    trap "exit 1" HUP INT TERM #
    ( #
      set +m #
      trap - EXIT #
      status=0 #
      IFS= read -r -t 5 status < <( #
        status=0 #
        "$@" -c "$probe" >"$probe_dir/output" 2>/dev/null </dev/null || status=$? #
        printf "%s\n" "$status" #
      ) || { #
        read_status=$? #
        if [ "$read_status" -gt 128 ]; then exit 124; fi #
        exit 125 #
      } #
      if [ "$status" != 0 ]; then exit 1; fi #
    ) & #
    # Monitor mode establishes this group before returning its PID. Its leader #
    # waits only for the builtin deadline, never for the stalled program. #
    probe_pid=$! #
    status=0 #
    wait "$probe_pid" 2>/dev/null || status=$? #
    case "$status" in 0) ;; 124 | 125) exit "$status" ;; *) exit 1 ;; esac #
    command -p cat "$probe_dir/output" || exit 125 #
  '                                                                                #
  "$probe_bash" -p -c "$probe_script" probe "$probe" "$@" 2>/dev/null || status=$? #
  case "$status" in 126 | 127) exit 125 ;; *) exit "$status" ;; esac               #
)                                                                                  #
#
# proven CANDIDATE [ARGUMENT]: it ran the probe and stated Python 3.floor or newer.
# The probe reads nothing: the hook's own standard input stays untouched.
proven() {                                                                                    #
  tried="${tried}${tried:+, }$*"                                                              #
  if stated=$(bounded_probe "$@"); then :; else                                               #
    status=$?                                                                                 #
    if [ "$status" -eq 124 ]; then                                                            #
      tried="$tried (version probe timed out after 5s)"                                       #
    fi                                                                                        #
    if [ "$status" -eq 125 ]; then                                                            #
      tried="$tried (version probe unavailable: temporary capture or Bash timed read failed)" #
    fi                                                                                        #
    return 1                                                                                  #
  fi                                                                                          #
  stated=${stated%"$cr"}                                                                      #
  case "$stated" in                                                                           #
    3.*) minor=${stated#3.} ;;                                                                #
    *) return 1 ;;                                                                            #
  esac                                                                                        #
  case "$minor" in                                                                            #
    '' | *[!0-9]*) return 1 ;;                                                                #
  esac                                                                                        #
  [ "$minor" -ge "$floor" ] || return 1                                                       #
  if [ "$resolve" = true ]; then                                                              #
    printf '%s\000' "$stated" "$@"                                                            #
    exit 0                                                                                    #
  fi                                                                                          #
  return 0                                                                                    #
}                                                                                             #
#
# Check Git's index before trusting an interpreter inside a virtualenv.
venv_candidate() { #
  directory=$1     #
  shift            #
  if tracked=$( #
    # Only a foreign worktree must drop the hook index; the checkout uses it. #
    if [ "$directory" != "$root" ]; then              #
      unset GIT_INDEX_FILE                            #
    fi                                                #
    git -C "$directory" ls-files -- .venv 2>/dev/null #
  ) && [ -z "$tracked" ]; then                        #
    for suffix in bin/python3 Scripts/python.exe; do  #
      venv="$directory/.venv/$suffix"                 #
      if proven "$venv"; then                         #
        exec "$venv" "$@"                             #
      fi                                              #
    done                                              #
  fi                                                  #
}                                                     #
#
main_worktree() {                                                                            #
  worktrees=$(git -c core.quotePath=false worktree list --porcelain 2>/dev/null) || return 1 #
  first=$(printf '%s\n' "$worktrees" | command -p head -n 1)                                 #
  case "$first" in                                                                           #
    "worktree "*) main=${first#worktree } ;;                                                 #
    *) return 1 ;;                                                                           #
  esac                                                                                       #
  # Only the first record describes the main repository; bare has no virtualenv. #
  if printf '%s\n' "$worktrees" | command -p sed '/^$/q' | command -p grep -qx bare; then #
    return 0                                                                              #
  fi                                                                                      #
  # Validate metadata instead of guessing from a directory suffix. Separate Git #
  # directories can resolve through core.worktree; absent metadata fails by name. #
  main=$(git -C "$main" rev-parse --show-toplevel 2>/dev/null) || return 1 #
  [ -n "$main" ] || return 1                                               #
  printf '%s\n' "$main"                                                    #
}                                                                          #
#
root=$(git rev-parse --show-toplevel 2>/dev/null) || root=''                            #
if [ -n "$root" ]; then                                                                 #
  venv_candidate "$root" "$@"                                                           #
  if ! main=$(main_worktree); then                                                      #
    echo "praetor hooks: cannot resolve main worktree from Git metadata for $root." >&2 #
    exit 125                                                                            #
  fi                                                                                    #
  if [ -n "$main" ] && [ "$main" != "$root" ]; then                                     #
    venv_candidate "$main" "$@"                                                         #
  fi                                                                                    #
fi                                                                                      #
#
if proven python3; then exec python3 "$@"; fi                                          #
if proven python; then exec python "$@"; fi                                            #
if proven py -3; then exec py -3 "$@"; fi                                              #
echo "praetor hooks: missing dependency: no Python 3.$floor or newer interpreter." >&2 #
echo "Tried: $tried; none answered the version probe." >&2                             #
echo "Install Python 3.$floor or newer under one of these names." >&2                  #
exit 127                                                                               #
