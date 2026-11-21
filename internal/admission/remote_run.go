// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package admission

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	snapshotTimeout = 5 * time.Minute
	remoteOverhead  = snapshotTimeout + 30*time.Second
	// MaxRemoteStepTimeout leaves room for transfer and cleanup in a Go duration.
	MaxRemoteStepTimeout = time.Duration(1<<63-1) - remoteOverhead - time.Second
	maxRemoteArgs        = 256
)

// RunRemote runs a noninteractive step in a disposable executor workspace. The SSH input
// carries the source bundle and stays open as a lease until the command ends.
func RunRemote(ctx context.Context, executor Executor, step Step, timeout time.Duration, jobs int, memoryMaxBytes uint64) (code int, resultErr error) {
	jobs, memoryMaxBytes, err := validateRemoteRun(executor, step, timeout, jobs, memoryMaxBytes)
	if err != nil {
		return 0, err
	}
	bundle, err := os.CreateTemp("", "praetor-admit-*.bundle")
	if err != nil {
		return 0, fmt.Errorf("executor %s: create temporary bundle: %w", executor.Name, err)
	}
	defer func() {
		resultErr = errors.Join(resultErr, bundle.Close(), os.Remove(bundle.Name()))
	}()
	snapshot, err := bundleSnapshot(ctx, bundle)
	if err != nil {
		return 0, fmt.Errorf("executor %s: prepare Git snapshot: %w", executor.Name, err)
	}
	return runRemoteSnapshot(ctx, executor, step, snapshot, bundle, timeout, jobs, memoryMaxBytes)
}

// validateRemoteRun validates executor configuration, arguments, limits, and timeout durations prior to execution.
func validateRemoteRun(executor Executor, step Step, timeout time.Duration, jobs int, memoryMaxBytes uint64) (int, uint64, error) {
	if err := executor.validate(); err != nil {
		return 0, 0, fmt.Errorf("executor %s: %w", executor.Name, err)
	}
	if len(step.Argv) == 0 || len(step.Argv) > maxRemoteArgs || timeout <= 0 || timeout > MaxRemoteStepTimeout {
		return 0, 0, fmt.Errorf("executor %s: invalid remote step arguments or timeout", executor.Name)
	}
	if err := step.Limits.Check(); err != nil {
		return 0, 0, err
	}
	if memoryMaxBytes == 0 {
		var err error
		memoryMaxBytes, err = LimitsMemoryMaxBytes(step.Limits)
		if err != nil {
			return 0, 0, err
		}
	}
	return max(1, jobs), memoryMaxBytes, nil
}

type gitSnapshot struct {
	head, tree, objectFormat, prefix string
	size                             int64
}

// bundleSnapshot creates a Git bundle stream containing HEAD and index objects for remote transmission.
func bundleSnapshot(ctx context.Context, bundle *os.File) (gitSnapshot, error) {
	ctx, cancel := context.WithTimeout(ctx, snapshotTimeout)
	defer cancel()
	snapshot, err := readSnapshot(ctx)
	if err != nil {
		return gitSnapshot{}, err
	}
	stderr, err := util.RunCommandStream(ctx, "", "git", nil, bundle, 4096,
		"--no-lazy-fetch", "--no-replace-objects", "-c", "pack.threads=1", "-c", "pack.windowMemory=64m",
		"bundle", "create", "--quiet", "-", "HEAD", snapshot.tree)
	if err != nil {
		return gitSnapshot{}, util.CommandDiagnostic(err, stderr)
	}
	ref, err := util.RunGit(ctx, "", "bundle", "list-heads", bundle.Name(), "HEAD")
	if err != nil {
		return gitSnapshot{}, err
	}
	if ref != snapshot.head+" HEAD" {
		return gitSnapshot{}, errors.New("HEAD changed while preparing the bundle")
	}
	snapshot.size, err = bundle.Seek(0, io.SeekEnd)
	if err != nil {
		return gitSnapshot{}, err
	}
	_, err = bundle.Seek(0, io.SeekStart)
	return snapshot, err
}

// readSnapshot reads Git commit and tree identifiers along with prefix path and object format information.
func readSnapshot(ctx context.Context) (gitSnapshot, error) {
	head, err := util.RunGit(ctx, "", "--no-lazy-fetch", "--no-replace-objects", "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return gitSnapshot{}, err
	}
	tree, err := util.RunGit(ctx, "", "--no-lazy-fetch", "write-tree")
	if err != nil {
		return gitSnapshot{}, err
	}
	if !util.ValidGitObjectID(head) || !util.ValidGitObjectID(tree) || len(head) != len(tree) {
		return gitSnapshot{}, errors.New("invalid HEAD or index object ID from git")
	}
	format := "sha1"
	if len(head) == 64 {
		format = "sha256"
	}
	out, err := util.RunGitBytes(ctx, "", 8192, "rev-parse", "--show-prefix")
	if err != nil {
		return gitSnapshot{}, util.CommandDiagnostic(err, out.Stderr)
	}
	return gitSnapshot{head: head, tree: tree, objectFormat: format, prefix: strings.TrimSuffix(string(out.Stdout), "\n")}, nil
}

// runRemoteSnapshot streams the Git bundle and executes the remote workspace command over SSH.
func runRemoteSnapshot(ctx context.Context, executor Executor, step Step, snapshot gitSnapshot, bundle *os.File, timeout time.Duration, jobs int, memoryMaxBytes uint64) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout+remoteOverhead+time.Second)
	defer cancel()
	input, lease, err := os.Pipe()
	if err != nil {
		return 0, fmt.Errorf("executor %s: open SSH input: %w", executor.Name, err)
	}
	transfer := make(chan error, 1)
	go func() {
		_, copyErr := io.Copy(lease, bundle)
		if copyErr != nil {
			copyErr = errors.Join(copyErr, lease.Close())
		}
		transfer <- copyErr
	}()
	command := remoteCommand(executor, step, snapshot, timeout, jobs, memoryMaxBytes)
	stepErr := &remoteStepStderr{inner: step.Stderr}
	sshErr := util.RunCommandPassthrough(ctx, "", "ssh", input, step.Stdout, stepErr, executor.sshArgs(command)...)
	closeErr := errors.Join(input.Close(), closeLease(lease))
	copyErr := <-transfer
	if sshErr != nil {
		code, err := remoteRunFailure(ctx, executor.Name, stepErr.stepFinished, sshErr)
		return code, errors.Join(err, closeErr)
	}
	return 0, errors.Join(copyErr, closeErr)
}

// closeLease closes an open lease file descriptor, ignoring already-closed errors.
func closeLease(lease *os.File) error {
	if err := lease.Close(); !errors.Is(err, os.ErrClosed) {
		return err
	}
	return nil
}

// remoteCommand formats the POSIX shell invocation for setting up and running the remote workspace step.
func remoteCommand(executor Executor, step Step, snapshot gitSnapshot, timeout time.Duration, jobs int, memoryMaxBytes uint64) string {
	stepArgs := append([]string{"timeout", "--kill-after=5s", timeoutSeconds(timeout)}, step.Argv...)
	args := []string{"timeout", "--kill-after=15s", timeoutSeconds(timeout + remoteOverhead), "sh", "-c", remoteWorkspaceScript,
		"praetor-admit", executor.Name, executor.WorkspaceRoot, strconv.FormatInt(snapshot.size, 10), snapshot.objectFormat,
		snapshot.head, snapshot.tree, snapshot.prefix, strconv.FormatUint(memoryMaxBytes, 10),
		strconv.Itoa(jobs)}
	args = append(args, stepArgs...)
	for i := 0; i < len(args) && i < maxRemoteArgs+40; i++ {
		args[i] = util.QuotePOSIXShellArg(args[i])
	}
	return strings.Join(args, " ")
}

// timeoutSeconds formats a time.Duration as a fractional seconds string suitable for the timeout command.
func timeoutSeconds(d time.Duration) string {
	return strconv.FormatFloat(d.Seconds(), 'f', 9, 64) + "s"
}

// One SSH session owns the directory. The pod cgroup is the memory barrier; the step does not
// run in a systemd user scope on the executor.
const remoteWorkspaceScript = `
set -eu
umask 077
execname=$1 root=$2 size=$3 format=$4 head=$5 tree=$6 prefix=$7 want=$8 jobs=$9
shift 9
bundle_fail() {
    head -c "$size" >/dev/null 2>&1 || :
}
cgroup=${PRAETOR_ADMIT_CGROUP_DIR:-/sys/fs/cgroup}
have=$(cat "$cgroup/memory.max" 2>/dev/null || echo __MISSING__)
case "$have" in
    max)
        bundle_fail
        echo "executor $execname: pod memory.max max is not the step MemoryMax $want" >&2
        exit 1
        ;;
    ''|*[!0-9]*)
        bundle_fail
        echo "executor $execname: pod memory.max unreadable is not the step MemoryMax $want" >&2
        exit 1
        ;;
esac
if [ "$have" != "$want" ]; then
    bundle_fail
    echo "executor $execname: pod memory.max $have is not the step MemoryMax $want" >&2
    exit 1
fi
run=$(mktemp -d -- "$root/praetor-admit.XXXXXXXXXX")
watcher= runner=
cleanup() {
    rc=$?
    trap - EXIT
    trap '' HUP INT TERM
    if [ -n "$watcher" ]; then
        kill -KILL "$watcher" 2>/dev/null || :
        wait "$watcher" 2>/dev/null || :
    fi
    if [ -n "$runner" ]; then
        kill -TERM "-$runner" "$runner" 2>/dev/null || :
        kill -KILL "-$runner" "$runner" 2>/dev/null || :
        wait "$runner" 2>/dev/null || :
    fi
    if ! rm -rf -- "$run"; then
        if [ "$rc" -eq 0 ]; then rc=1; fi
    fi
    exit "$rc"
}
trap cleanup EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM
cd -- "$run"
physical=$(pwd -P)
git init -q --template= --object-format="$format"
head -c "$size" | git bundle unbundle - >/dev/null
git update-ref --no-deref HEAD "$head"
git read-tree --reset -u "$tree"
cd -- "./$prefix"
case $(pwd -P)/ in
    "$physical/"*) ;;
    *) echo 'working directory escapes the remote workspace' >&2; exit 1 ;;
esac
exec 3<&0
parent=$$
( IFS= read -r unused <&3 || kill -TERM "$parent" ) &
watcher=$!
export MAKEFLAGS=-j"$jobs" GOMAXPROCS="$jobs"
"$@" </dev/null 3<&- &
runner=$!
rc=0
wait "$runner" || rc=$?
runner=
printf 'praetor-admit-step-complete %s\n' "$rc" >&2
exit "$rc"
`
