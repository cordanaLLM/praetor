package repairrun

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/praetor/internal/sandbox"
)

// TestResult records scoped execution, not original private-corpus verification.
type TestResult struct {
	Passed    bool          `json:"passed"`
	ExitCode  int           `json:"exit_code"`
	LogSHA256 string        `json:"log_sha256"`
	LogBytes  int           `json:"log_bytes"`
	Packages  []string      `json:"packages"`
	Tests     []TestOutcome `json:"tests"`
}

func verifyWorkspace(ctx context.Context, cfg Config, candidate string) (*TestResult, []byte, error) {
	spec, err := verificationSpec(ctx, cfg, candidate)
	if err != nil {
		return nil, nil, err
	}
	invocation, err := (sandbox.Runner{}).Command(ctx, spec)
	if err != nil {
		return nil, nil, err
	}
	data, runErr := command(ctx, "", invocation.Environment, maxLogBytes, invocation.Path, invocation.Args...)
	result := &TestResult{Passed: runErr == nil, ExitCode: 0, LogSHA256: bytesSHA(data), LogBytes: len(data), Packages: cfg.TestPackages}
	result.Tests, err = testOutcomes(data)
	if err != nil {
		result.Passed = false
		return result, data, err
	}
	if runErr == nil {
		if !allTestsPassed(result.Tests) {
			result.Passed = false
			return result, data, errors.New("successful process did not pass its observed tests")
		}
		return result, data, nil
	}
	var exit *exec.ExitError
	if !errors.As(runErr, &exit) || ctx.Err() != nil {
		return result, data, errors.New("verification process did not complete within bounds")
	}
	result.ExitCode = exit.ExitCode()
	if !containsFailedTest(result.Tests) {
		return result, data, errors.New("verification failed before a reproduced Go test failure")
	}
	return result, data, nil
}

func verificationSpec(ctx context.Context, cfg Config, candidate string) (sandbox.Spec, error) {
	goPath, root, err := goToolchainRoot(ctx)
	if err != nil {
		return sandbox.Spec{}, err
	}
	cache, err := moduleCache(ctx)
	if err != nil {
		return sandbox.Spec{}, err
	}
	spec := sandbox.Spec{Workspace: candidate, ReadOnly: true,
		ReadOnlyBinds: []sandbox.Bind{{Source: cache, Target: "/deps"}, {Source: root, Target: root}},
		Environment: []sandbox.Variable{{Name: "HOME", Value: "/home"}, {Name: "PATH", Value: "/usr/bin:/bin"},
			{Name: "TMPDIR", Value: "/tmp"}, {Name: "GOCACHE", Value: "/tmp/go-cache"}, {Name: "GOMODCACHE", Value: "/deps"},
			{Name: "GOPATH", Value: "/tmp/go"}, {Name: "GOPROXY", Value: "off"}, {Name: "GOSUMDB", Value: "off"},
			{Name: "GOTOOLCHAIN", Value: "local"}, {Name: "GOTELEMETRY", Value: "off"}, {Name: "GOMAXPROCS", Value: "2"}, {Name: "CGO_ENABLED", Value: "0"}},
		Command: []string{goPath, "test", "-json", "-count=1", "-mod=readonly", "-p=2", "-timeout=60s"}}
	spec.Command = append(spec.Command, cfg.TestPackages...)
	return spec, nil
}

// goToolchainRoot resolves the go binary from PATH (goBinary) and its GOROOT, so the sandbox
// can bind the toolchain that is actually installed instead of assuming /usr/bin/go. It
// returns the binary's path exactly as bwrap must see it (goPath lives under root, since
// GOROOT/bin/go is go's own layout contract) plus root for the --ro-bind call.
//
// Both are compared and returned in their symlink-resolved form. PATH usually reaches go
// through a link -- /usr/bin/go -> /usr/lib/go/bin/go on Arch, Debian's golang-go
// alternatives, Homebrew's /opt/homebrew/bin/go -- while go reports GOROOT from its own
// resolved executable. Comparing the link against GOROOT refused every such host, and the
// failure hid under `go test`, which puts GOROOT/bin first on PATH so LookPath found the
// real binary there and nowhere else.
func goToolchainRoot(ctx context.Context) (goPath, root string, err error) {
	linked, err := goBinary()
	if err != nil {
		return "", "", err
	}
	data, err := command(ctx, "", []string{"PATH=" + filepath.Dir(linked)}, 4096, linked, "env", "GOROOT")
	if err != nil {
		return "", "", errors.New("cannot discover installed Go toolchain root")
	}
	reported := strings.TrimSpace(string(data))
	if !cleanAbsolute(reported) {
		return "", "", errors.New("invalid installed Go toolchain root")
	}
	if goPath, err = filepath.EvalSymlinks(linked); err != nil {
		return "", "", fmt.Errorf("resolve installed go binary: %w", err)
	}
	if root, err = filepath.EvalSymlinks(reported); err != nil {
		return "", "", fmt.Errorf("resolve installed Go toolchain root: %w", err)
	}
	if !strings.HasPrefix(goPath, root+string(filepath.Separator)) {
		return "", "", errors.New("installed go binary is not under its own GOROOT")
	}
	return goPath, root, nil
}

func moduleCache(ctx context.Context) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	goPath, err := goBinary()
	if err != nil {
		return "", err
	}
	data, err := command(ctx, "", []string{"HOME=" + home, "PATH=" + filepath.Dir(goPath)}, 4096, goPath, "env", "GOMODCACHE")
	if err != nil {
		return "", errors.New("cannot discover installed Go module cache")
	}
	path := strings.TrimSpace(string(data))
	if !cleanAbsolute(path) || filepath.Base(path) != "mod" {
		return "", errors.New("invalid installed Go module cache path")
	}
	root, err := openDirectory(path)
	if err != nil {
		return "", err
	}
	return path, root.Close()
}
