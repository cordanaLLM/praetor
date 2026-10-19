package sandbox

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
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

// Runner's zero value uses the host OS, PATH lookup and a real user-manager
// probe. Injection supports deterministic failure tests without executing agents.
type Runner struct {
	GOOS             string
	Lookup           func(string) (string, error)
	CheckUserManager func(context.Context) error
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
	return invocation, nil
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
	if err := util.RunCommandPassthrough(ctx, "", invocation.Path, stdin, stdout, stderr, invocation.Args...); err != nil {
		return fmt.Errorf("sandbox execution: %w", err)
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
