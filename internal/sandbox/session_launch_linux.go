// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package sandbox

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/cordanaLLM/praetor/internal/admission"
	"github.com/cordanaLLM/praetor/internal/util"
)

const fifoOpener = `exec 3>"$1" 4<"$2"; shift 2; exec "$@"`

// sessionInvocation wraps the sandbox invocation with FIFO descriptors and adds the pasta PID file when needed to identify the host process.
func sessionInvocation(inv Invocation, sh, info, block, pidfile string, command []string) (Invocation, bool, error) {
	args := slices.Clone(inv.Args)
	end := len(args) - len(command) - 1
	if end < 0 || args[end] != "--" {
		return Invocation{}, false, errors.New("invalid test-exec invocation")
	}
	args = slices.Insert(args, end, "--info-fd", "3", "--block-fd", "4")
	script := slices.Index(args, namespaceRangeScript)
	if script < 0 {
		return Invocation{Path: sh, Args: append([]string{"-c", fifoOpener, "praetor-test-exec", info, block, inv.Path}, args...)}, false, nil
	}
	args[script] = strings.Replace(namespaceRangeScript, `shift 2; exec "$@"`, `shift 2; `+fifoOpener, 1)
	args = slices.Insert(args, script+4, info, block)
	separator := slices.Index(args, "--")
	if separator < 0 {
		return Invocation{}, false, errors.New("missing pasta separator")
	}
	args = slices.Insert(args, separator, "--pid", pidfile)
	return Invocation{Path: inv.Path, Args: args}, true, nil
}

type sessionFiles struct {
	dir, registry, pid string
	info, block        *os.File
}

// newSessionFiles creates private handshake FIFOs and registry paths, cleaning up partial setup on failure.
func newSessionFiles() (result *sessionFiles, resultErr error) {
	root := os.Getenv("XDG_RUNTIME_DIR")
	if root == "" {
		var err error
		root, err = privateSessionRoot(os.TempDir())
		if err != nil {
			return nil, err
		}
	}
	dir, err := os.MkdirTemp(root, fmt.Sprintf("praetor-testexec-%d-", os.Getuid()))
	if err != nil {
		return nil, err
	}
	s := &sessionFiles{dir: dir, registry: filepath.Join(root, fmt.Sprintf("praetor-testexec-%d.registry", os.Getuid())), pid: filepath.Join(dir, "pasta.pid")}
	rootDir, err := os.OpenRoot(dir)
	if err != nil {
		return nil, errors.Join(err, removeSessionDir(dir))
	}
	defer func() {
		if closeErr := rootDir.Close(); closeErr != nil {
			resultErr = errors.Join(resultErr, closeErr)
		}
	}()
	var files [2]*os.File
	for i, name := range []string{"info", "block"} {
		path := filepath.Join(dir, name)
		if err = syscall.Mkfifo(path, 0600); err == nil {
			files[i], err = rootDir.OpenFile(name, os.O_RDWR|syscall.O_NONBLOCK, 0600)
		}
		if err != nil {
			err = errors.Join(err, closeSessionFiles(files), removeSessionDir(dir))
			return nil, err
		}
	}
	s.info, s.block = files[0], files[1]
	return s, nil
}

// privateSessionRoot creates and validates the user's registry directory in the shared temporary root.
func privateSessionRoot(temp string) (string, error) {
	path := filepath.Join(temp, fmt.Sprintf("praetor-testexec-%d", os.Getuid()))
	// nosemgrep: go.lang.correctness.permissions.file_permission.incorrect-default-permission -- the private directory needs the owner search bit
	if err := os.Mkdir(path, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return "", fmt.Errorf("private session directory: %w", err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return "", fmt.Errorf("private session directory: %w", err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() || info.Mode().Perm() != 0700 || int64(stat.Uid) != int64(os.Getuid()) {
		return "", errors.New("invalid private session directory")
	}
	return path, nil
}

// closeSessionFiles closes any opened handshake files and combines errors so partial setup can be cleaned up.
func closeSessionFiles(files [2]*os.File) error {
	var result error
	for _, file := range files {
		if file != nil {
			result = errors.Join(result, file.Close())
		}
	}
	return result
}

// close closes the handshake files and removes their directory so each launch releases its resources.
func (s *sessionFiles) close() error {
	return errors.Join(s.info.Close(), s.block.Close(), removeSessionDir(s.dir))
}

// removeSessionDir removes the known session files and directory without recursively deleting unrelated paths.
func removeSessionDir(dir string) (resultErr error) {
	root, err := os.OpenRoot(filepath.Dir(dir))
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, root.Close()) }()
	base := filepath.Base(dir)
	for _, name := range []string{"info", "block", "pasta.pid"} {
		err := root.Remove(filepath.Join(base, name))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			resultErr = errors.Join(resultErr, err)
		}
	}
	return errors.Join(resultErr, root.Remove(base))
}

const sessionAttempts = 16

// runSession launches a session with bounded retries so start-tick collisions cannot reuse an identity.
func runSession(ctx context.Context, inv Invocation, sh string, command []string, stdin io.Reader, stdout, stderr io.Writer) error {
	return retrySession(ctx, func() (bool, error) { return runSessionAttempt(ctx, inv, sh, command, stdin, stdout, stderr) }, admission.SleepContext)
}

// retrySession retries collisions up to sixteen attempts with jitter and stops on cancellation or other errors.
func retrySession(ctx context.Context, attempt func() (bool, error), wait func(context.Context, time.Duration) error) error {
	for i := 0; i < sessionAttempts; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		collision, err := attempt()
		if err != nil {
			return err
		}
		if !collision {
			return nil
		}
		if i+1 < sessionAttempts {
			value, err := rand.Int(rand.Reader, big.NewInt(31))
			if err != nil {
				return fmt.Errorf("session collision jitter: %w", err)
			}
			delay := time.Duration(10+value.Int64()) * time.Millisecond
			if err := wait(ctx, delay); err != nil {
				return fmt.Errorf("session collision retry: %w", err)
			}
		}
	}
	return fmt.Errorf("session identity collision: %d attempts exhausted", sessionAttempts)
}

// runSessionAttempt reserves the sandbox start tick before releasing its handshake and marks it released after the process exits.
func runSessionAttempt(ctx context.Context, inv Invocation, sh string, command []string, stdin io.Reader, stdout, stderr io.Writer) (collision bool, resultErr error) {
	files, err := newSessionFiles()
	if err != nil {
		return false, err
	}
	defer func() { resultErr = errors.Join(resultErr, files.close()) }()
	wrapped, container, err := sessionInvocation(inv, sh, filepath.Join(files.dir, "info"), filepath.Join(files.dir, "block"), files.pid, command)
	if err != nil {
		return false, err
	}
	childCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- util.RunCommandPassthrough(childCtx, "", wrapped.Path, stdin, stdout, stderr, wrapped.Args...)
	}()
	handshake, stop := context.WithTimeout(childCtx, 5*time.Second)
	defer stop()
	tick, err := sessionTick(handshake, files, container, done)
	if err != nil {
		cancel()
		childErr := <-done
		return false, errors.Join(err, childErr)
	}
	reserved, err := reserveSessionTick(handshake, files.registry, tick, os.Getpid())
	if err != nil || !reserved {
		cancel()
		childErr := <-done
		if err != nil {
			return false, errors.Join(err, childErr)
		}
		return true, nil
	}
	defer func() {
		_, err := updateSessionRegistry(context.WithoutCancel(ctx), files.registry, tick, os.Getpid(), true)
		resultErr = errors.Join(resultErr, err)
	}()
	if _, err = files.block.Write([]byte{1}); err != nil {
		cancel()
		childErr := <-done
		return false, errors.Join(fmt.Errorf("release session sandbox: %w", err), childErr)
	}
	return false, <-done
}

// sessionTick resolves the sandbox child to a host PID when needed and reads its start tick for reservation.
func sessionTick(ctx context.Context, files *sessionFiles, container bool, done chan error) (uint64, error) {
	child, err := readSessionInfo(ctx, files.info, done)
	if err != nil {
		return 0, err
	}
	if container {
		child, err = hostSessionPID(ctx, files.pid, child)
		if err != nil {
			return 0, err
		}
	}
	return procStartTick("/proc", child)
}

// readSessionInfo reads bounded handshake data and waits at most 500 times for a valid child PID.
func readSessionInfo(ctx context.Context, file *os.File, done chan error) (int, error) {
	data := make([]byte, 0, 4096)
	buf := make([]byte, 4096)
	for i := 0; i < 500; i++ {
		fd := int(file.Fd())
		if err := syscall.SetNonblock(fd, true); err != nil {
			return 0, err
		}
		n, err := syscall.Read(fd, buf[len(data):])
		if n > 0 {
			data = append(data, buf[len(data):len(data)+n]...)
		}
		var info struct {
			ChildPID int `json:"child-pid"`
		}
		if json.Unmarshal(data, &info) == nil && info.ChildPID > 0 {
			return info.ChildPID, nil
		}
		if len(data) == cap(data) {
			return 0, errors.New("bwrap session info exceeds limit")
		}
		if err != nil && !errors.Is(err, syscall.EAGAIN) {
			return 0, err
		}
		if err = waitSessionInfo(ctx, done); err != nil {
			return 0, err
		}
	}
	return 0, errors.New("bwrap session info: wait limit exceeded")
}

// waitSessionInfo waits for handshake data while detecting cancellation or premature sandbox exit.
func waitSessionInfo(ctx context.Context, done chan error) error {
	select {
	case <-ctx.Done():
		return fmt.Errorf("bwrap session info: %w", ctx.Err())
	case err := <-done:
		done <- err
		if err == nil {
			return errors.New("bwrap exited before session info")
		}
		return fmt.Errorf("bwrap exited before session info: %w", err)
	case <-time.After(10 * time.Millisecond):
		return nil
	}
}

// hostSessionPID waits for the pasta PID file with a bounded retry count and resolves the namespace child to a host PID.
func hostSessionPID(ctx context.Context, pidfile string, target int) (int, error) {
	for i := 0; i < 500; i++ {
		data, err := util.ReadFileLimited(pidfile, 64)
		if err == nil {
			pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
			if err != nil || pid < 1 {
				return 0, fmt.Errorf("invalid pasta pid: %q", data)
			}
			return findSessionInit("/proc", pid, target)
		}
		if !errors.Is(err, os.ErrNotExist) {
			return 0, err
		}
		select {
		case <-ctx.Done():
			return 0, fmt.Errorf("pasta pid: %w", ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	return 0, errors.New("pasta pid: wait limit exceeded")
}

// findSessionInit walks at most three process-tree levels to find the init matching the reported namespace PID.
func findSessionInit(root string, parent, target int) (int, error) {
	current := []int{parent}
	for depth := 0; depth < 3; depth++ {
		next, err := sessionChildrenLevel(root, current)
		if err != nil {
			return 0, fmt.Errorf("session children walk: %w", err)
		}
		for _, pid := range next {
			match, err := sessionPIDMatches(root, pid, target)
			if err != nil {
				return 0, fmt.Errorf("session children walk: %w", err)
			}
			if match {
				return pid, nil
			}
		}
		current = next
	}
	return 0, fmt.Errorf("session children walk: no NSpid match for %d", target)
}

// sessionChildrenLevel collects one process-tree level and rejects more than 64 children to bound the walk.
func sessionChildrenLevel(root string, current []int) ([]int, error) {
	next := []int{}
	for _, pid := range current {
		children, err := procChildren(root, pid)
		if err != nil {
			return nil, err
		}
		next = append(next, children...)
		if len(next) > 64 {
			return nil, errors.New("child limit exceeded")
		}
	}
	return next, nil
}

// sessionPIDMatches checks namespace PID fields for the reported child and its init without accepting unrelated processes.
func sessionPIDMatches(root string, pid, target int) (bool, error) {
	data, err := util.ReadFileLimited(filepath.Join(root, strconv.Itoa(pid), "status"), 65536)
	if err != nil {
		return false, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		value, ok := strings.CutPrefix(line, "NSpid:")
		if !ok {
			continue
		}
		fields := strings.Fields(value)
		return len(fields) >= 2 && fields[len(fields)-1] == "1" && fields[len(fields)-2] == strconv.Itoa(target), nil
	}
	return false, nil
}

// procChildren reads task children with limits on tasks, input size, and child count to bound process discovery.
func procChildren(root string, pid int) ([]int, error) {
	task := filepath.Join(root, strconv.Itoa(pid), "task")
	dirs, err := os.ReadDir(task)
	if err != nil {
		return nil, err
	}
	if len(dirs) > 64 {
		return nil, errors.New("task limit exceeded")
	}
	var children []int
	for _, dir := range dirs {
		data, err := util.ReadFileLimited(filepath.Join(task, dir.Name(), "children"), 4096)
		if err != nil {
			return nil, err
		}
		for _, field := range strings.Fields(string(data)) {
			child, err := strconv.Atoi(field)
			if err != nil || child < 1 {
				return nil, fmt.Errorf("invalid child pid %q", field)
			}
			children = append(children, child)
			if len(children) > 64 {
				return nil, errors.New("child limit exceeded")
			}
		}
	}
	return children, nil
}

// procStartTick extracts the start tick from a bounded proc stat record and rejects malformed or missing data.
func procStartTick(root string, pid int) (uint64, error) {
	data, err := util.ReadFileLimited(filepath.Join(root, strconv.Itoa(pid), "stat"), 65536)
	if err != nil {
		return 0, fmt.Errorf("read session start tick: %w", err)
	}
	pos := strings.LastIndex(string(data), ") ")
	if pos < 0 {
		return 0, errors.New("invalid session stat")
	}
	rest, ok := string(data)[pos+2:], true
	if !ok {
		return 0, errors.New("invalid session stat")
	}
	fields := strings.Fields(rest)
	if len(fields) < 20 {
		return 0, errors.New("short session stat")
	}
	tick, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid session start tick: %w", err)
	}
	return tick, nil
}
