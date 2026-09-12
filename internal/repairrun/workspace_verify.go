package repairrun

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
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
	args, err := verificationArguments(ctx, cfg, candidate)
	if err != nil {
		return nil, nil, err
	}
	limited := append([]string{"--user", "--scope", "--quiet", "--collect", "--expand-environment=no", "--property=MemoryMax=4G", "--property=MemorySwapMax=0", "--property=TasksMax=128", "--property=CPUQuota=200%", "/usr/bin/prlimit", "--as=4294967296", "--cpu=120", "--fsize=268435456", "--nofile=256", "--", "/usr/bin/bwrap"}, args...)
	env := []string{"PATH=/usr/bin:/bin", "XDG_RUNTIME_DIR=/run/user/" + strconv.Itoa(os.Getuid())}
	data, runErr := command(ctx, "", env, maxLogBytes, "/usr/bin/systemd-run", limited...)
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

func verificationArguments(ctx context.Context, cfg Config, candidate string) ([]string, error) {
	if runtime.GOOS != "linux" {
		return nil, errors.New("repair verification requires Linux bubblewrap")
	}
	cache, err := moduleCache(ctx)
	if err != nil {
		return nil, err
	}
	args := []string{"--unshare-all", "--die-with-parent", "--new-session", "--clearenv", "--ro-bind", "/usr", "/usr", "--symlink", "usr/bin", "/bin", "--symlink", "usr/lib", "/lib", "--symlink", "usr/lib", "/lib64", "--proc", "/proc", "--dev", "/dev", "--size", "1073741824", "--tmpfs", "/tmp", "--dir", "/home", "--ro-bind", candidate, "/workspace", "--ro-bind", cache, "/deps", "--chdir", "/workspace"}
	for _, entry := range [][2]string{{"HOME", "/home"}, {"PATH", "/usr/bin:/bin"}, {"TMPDIR", "/tmp"}, {"GOCACHE", "/tmp/go-cache"}, {"GOMODCACHE", "/deps"}, {"GOPATH", "/tmp/go"}, {"GOPROXY", "off"}, {"GOSUMDB", "off"}, {"GOTOOLCHAIN", "local"}, {"GOTELEMETRY", "off"}, {"GOMAXPROCS", "2"}, {"CGO_ENABLED", "0"}} {
		args = append(args, "--setenv", entry[0], entry[1])
	}
	args = append(args, "--", "/usr/bin/go", "test", "-json", "-count=1", "-mod=readonly", "-p=2", "-timeout=60s")
	return append(args, cfg.TestPackages...), nil
}

func moduleCache(ctx context.Context) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	data, err := command(ctx, "", []string{"HOME=" + home, "PATH=/usr/bin:/bin"}, 4096, "/usr/bin/go", "env", "GOMODCACHE")
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
