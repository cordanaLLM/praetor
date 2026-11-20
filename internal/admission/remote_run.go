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
	"os/exec"
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
func RunRemote(ctx context.Context, executor Executor, step Step, timeout time.Duration) (code int, resultErr error) {
	if err := executor.validate(); err != nil {
		return 0, fmt.Errorf("executor %s: %w", executor.Name, err)
	}
	if len(step.Argv) == 0 || len(step.Argv) > maxRemoteArgs || timeout <= 0 || timeout > MaxRemoteStepTimeout {
		return 0, fmt.Errorf("executor %s: invalid remote step arguments or timeout", executor.Name)
	}
	if err := step.Limits.Check(); err != nil {
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
	return runRemoteSnapshot(ctx, executor, step, snapshot, bundle, timeout)
}

type gitSnapshot struct {
	head, tree, objectFormat, prefix string
	size                             int64
}

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

func runRemoteSnapshot(ctx context.Context, executor Executor, step Step, snapshot gitSnapshot, bundle *os.File, timeout time.Duration) (int, error) {
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
	command := remoteCommand(executor, step, snapshot, timeout)
	sshErr := util.RunCommandPassthrough(ctx, "", "ssh", input, step.Stdout, step.Stderr, executor.sshArgs(command)...)
	closeErr := errors.Join(input.Close(), closeLease(lease))
	copyErr := <-transfer
	if sshErr != nil {
		code, err := remoteRunStatus(ctx, executor, sshErr)
		return code, errors.Join(err, closeErr)
	}
	return 0, errors.Join(copyErr, closeErr)
}

func closeLease(lease *os.File) error {
	if err := lease.Close(); !errors.Is(err, os.ErrClosed) {
		return err
	}
	return nil
}

func remoteRunStatus(ctx context.Context, executor Executor, err error) (int, error) {
	var child *exec.ExitError
	if ctx.Err() != nil || !errors.As(err, &child) || child.ExitCode() <= 0 {
		return 0, fmt.Errorf("executor %s: SSH run: %w", executor.Name, err)
	}
	if child.ExitCode() == 255 {
		return 255, fmt.Errorf("executor %s: SSH returned status 255", executor.Name)
	}
	return child.ExitCode(), nil
}

func remoteCommand(executor Executor, step Step, snapshot gitSnapshot, timeout time.Duration) string {
	unit := UnitName(step.Label, time.Now())
	stepArgs := append([]string{"timeout", "--kill-after=5s", timeoutSeconds(timeout)}, step.Argv...)
	args := []string{"timeout", "--kill-after=15s", timeoutSeconds(timeout + remoteOverhead), "sh", "-c", remoteWorkspaceScript,
		"praetor-admit", executor.WorkspaceRoot, strconv.FormatInt(snapshot.size, 10), snapshot.objectFormat,
		snapshot.head, snapshot.tree, snapshot.prefix, unit + ".scope"}
	args = append(args, "systemd-run")
	args = append(args, ScopeArgs(unit, step.Limits, stepArgs)...)
	for i := 0; i < len(args) && i < maxRemoteArgs+40; i++ {
		args[i] = util.QuotePOSIXShellArg(args[i])
	}
	return strings.Join(args, " ")
}

func timeoutSeconds(d time.Duration) string {
	return strconv.FormatFloat(d.Seconds(), 'f', 9, 64) + "s"
}

// One SSH session owns the directory. After receiving the bundle, a read on the lease
// detects disconnects even for a step that never reads stdin or produces output.
const remoteWorkspaceScript = `
set -eu
umask 077
root=$1 size=$2 format=$3 head=$4 tree=$5 prefix=$6 scope=$7
shift 7
run=$(mktemp -d -- "$root/praetor-admit.XXXXXXXXXX")
watcher= runner=
cleanup() {
    rc=$?
    trap - EXIT
    trap '' HUP INT TERM
    if [ -n "$watcher" ]; then
        kill "$watcher" 2>/dev/null || :
    fi
    if [ -n "$runner" ]; then
        kill "$runner" 2>/dev/null || :
        timeout --kill-after=2s 10s systemctl --user stop "$scope" || :
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
"$@" </dev/null 3<&- &
runner=$!
rc=0
wait "$runner" || rc=$?
runner=
exit "$rc"
`
