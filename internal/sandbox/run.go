package sandbox

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

// ErrUnavailable identifies a platform, executable or manager that cannot sandbox.
var ErrUnavailable = errors.New("sandbox unavailable")

// UnavailableError names the missing sandbox facility and retains its cause.
type UnavailableError struct {
	Facility string
	Cause    error
}

func (e *UnavailableError) Error() string {
	return fmt.Sprintf("sandbox unavailable: %s: %v", e.Facility, e.Cause)
}

func (e *UnavailableError) Unwrap() error { return e.Cause }

func (e *UnavailableError) Is(target error) bool { return target == ErrUnavailable }

// Runner's zero value uses the host OS, PATH lookup, a user-manager probe and a
// bubblewrap namespace probe. Injection supports deterministic failure tests
// without executing agents.
type Runner struct {
	GOOS             string
	Lookup           func(string) (string, error)
	CheckUserManager func(context.Context) error
	ProbeBwrap       func(context.Context) error
}

// Command validates the workspace and runtime before returning a contained argv.
// It never returns an invocation of the requested command directly.
func (r Runner) Command(ctx context.Context, spec Spec) (Invocation, error) {
	if ctx == nil {
		return Invocation{}, errors.New("sandbox command requires a context")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	binaries, err := r.resolve()
	if err != nil {
		return Invocation{}, err
	}
	invocation, err := Build(spec, binaries, os.Environ())
	if err != nil {
		return Invocation{}, err
	}
	if err := workspaceDirectory(ctx, spec.Workspace); err != nil {
		return Invocation{}, err
	}
	invocation.Environment, err = ManagerEnvironment()
	if err != nil {
		return Invocation{}, err
	}
	check := r.CheckUserManager
	if check == nil {
		check = r.checkManager
	}
	if err := check(ctx); err != nil {
		return Invocation{}, &UnavailableError{Facility: "systemd user manager", Cause: err}
	}
	probe := r.ProbeBwrap
	if probe == nil {
		probe = func(ctx context.Context) error {
			return r.probeBwrap(ctx, binaries.Bwrap)
		}
	}
	if err := probe(ctx); err != nil {
		return Invocation{}, &UnavailableError{Facility: "bwrap", Cause: err}
	}
	return invocation, nil
}

const maxExecutionStderrBytes = 4096

type tailWriter struct {
	limit int
	buf   []byte
}

func (w *tailWriter) Write(p []byte) (int, error) {
	if w.limit <= 0 {
		return len(p), nil
	}
	if len(p) >= w.limit {
		w.buf = append(w.buf[:0], p[len(p)-w.limit:]...)
		return len(p), nil
	}
	overflow := len(w.buf) + len(p) - w.limit
	if overflow > 0 {
		copy(w.buf, w.buf[overflow:])
		w.buf = w.buf[:len(w.buf)-overflow]
	}
	w.buf = append(w.buf, p...)
	return len(p), nil
}

func (w *tailWriter) String() string {
	return string(w.buf)
}

func executionError(err error, stderr string) error {
	diagnostic := strings.TrimSpace(stderr)
	if diagnostic != "" {
		return fmt.Errorf("sandbox execution: %w: %s", err, diagnostic)
	}
	return fmt.Errorf("sandbox execution: %w", err)
}

// Run executes only Command's sandbox invocation through the audited exec boundary.
// Process exit errors are preserved so callers can return the command's status.
func (r Runner) Run(ctx context.Context, spec Spec, stdin io.Reader, stdout, stderr io.Writer) error {
	if ctx == nil {
		return errors.New("sandbox run requires a context")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	invocation, err := r.Command(ctx, spec)
	if err != nil {
		return err
	}
	ctx, err = util.WithCommandEnvironment(ctx, invocation.Environment)
	if err != nil {
		return fmt.Errorf("sandbox manager environment: %w", err)
	}
	tail := &tailWriter{limit: maxExecutionStderrBytes}
	combinedStderr := io.Writer(tail)
	if stderr != nil {
		combinedStderr = io.MultiWriter(stderr, tail)
	}
	if err := util.RunCommandPassthrough(ctx, "", invocation.Path, stdin, stdout, combinedStderr, invocation.Args...); err != nil {
		return executionError(err, tail.String())
	}
	return nil
}

func (r Runner) lookup(name string) (string, error) {
	lookup := r.Lookup
	if lookup == nil {
		lookup = exec.LookPath
	}
	value, err := lookup(name)
	if err != nil {
		return "", &UnavailableError{Facility: name, Cause: err}
	}
	if !absolutePath(value) {
		return "", &UnavailableError{Facility: name, Cause: errors.New("lookup did not return a clean absolute path")}
	}
	return value, nil
}

func (r Runner) resolve() (Binaries, error) {
	goos := r.GOOS
	if goos == "" {
		goos = runtime.GOOS
	}
	if goos != "linux" {
		return Binaries{}, &UnavailableError{Facility: "Linux", Cause: fmt.Errorf("unsupported OS %s", goos)}
	}
	bwrap, err := r.lookup("bwrap")
	if err != nil {
		return Binaries{}, err
	}
	systemd, err := r.lookup("systemd-run")
	if err != nil {
		return Binaries{}, err
	}
	prlimit, err := r.lookup("prlimit")
	if err != nil {
		return Binaries{}, err
	}
	return Binaries{SystemdRun: systemd, Prlimit: prlimit, Bwrap: bwrap}, nil
}

func (r Runner) checkManager(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	systemctl, err := r.lookup("systemctl")
	if err != nil {
		return err
	}
	env, err := ManagerEnvironment()
	if err != nil {
		return err
	}
	ctx, err = util.WithCommandEnvironment(ctx, env)
	if err != nil {
		return fmt.Errorf("user manager environment: %w", err)
	}
	_, err = util.RunCommandBytes(ctx, "", systemctl, 4096, "--user", "show", "--property=Version", "--value")
	if err != nil {
		return fmt.Errorf("cannot contact user manager: %w", err)
	}
	return nil
}

func (r Runner) probeBwrap(ctx context.Context, bwrap string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	probeTarget := "/usr/bin/true"
	if _, err := os.Stat(probeTarget); err != nil {
		probeTarget = "/bin/true"
	}
	result, err := util.RunCommandBytes(ctx, "", bwrap, 4096,
		"--unshare-all", "--die-with-parent",
		"--ro-bind", "/usr", "/usr",
		"--symlink", "usr/bin", "/bin",
		"--symlink", "usr/lib", "/lib",
		"--symlink", "usr/lib", "/lib64",
		"--proc", "/proc",
		"--dev", "/dev",
		probeTarget,
	)
	if err != nil {
		return util.CommandDiagnostic(err, result.Stderr)
	}
	return nil
}

func workspaceDirectory(ctx context.Context, workspace string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("sandbox workspace: %w", err)
	}
	info, err := os.Stat(workspace)
	if err != nil {
		return fmt.Errorf("sandbox workspace: %w", err)
	}
	if !info.IsDir() {
		return errors.New("sandbox workspace must be a directory")
	}
	return nil
}
