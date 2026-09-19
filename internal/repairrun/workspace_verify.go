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
	goPath, root, err := goToolchainRoot(ctx)
	if err != nil {
		return nil, err
	}
	cache, err := moduleCache(ctx)
	if err != nil {
		return nil, err
	}
	// The sandbox binds "/usr" plus the resolved toolchain root read-only, rather than
	// assuming go lives inside "/usr": actions/setup-go installs to a hosted tool cache
	// (e.g. /opt/hostedtoolcache/go/<version>/x64), not to /usr/bin/go, and a hardcoded
	// /usr/bin/go silently downgraded to a stale distro Go -- or none at all -- once a CI
	// image actually exercised this real-bubblewrap path (praetor#376).
	args := []string{"--unshare-all", "--die-with-parent", "--new-session", "--clearenv", "--ro-bind", "/usr", "/usr", "--symlink", "usr/bin", "/bin", "--symlink", "usr/lib", "/lib", "--symlink", "usr/lib", "/lib64", "--proc", "/proc", "--dev", "/dev", "--size", "1073741824", "--tmpfs", "/tmp", "--dir", "/home", "--ro-bind", candidate, "/workspace", "--ro-bind", cache, "/deps", "--ro-bind", root, root, "--chdir", "/workspace"}
	for _, entry := range [][2]string{{"HOME", "/home"}, {"PATH", "/usr/bin:/bin"}, {"TMPDIR", "/tmp"}, {"GOCACHE", "/tmp/go-cache"}, {"GOMODCACHE", "/deps"}, {"GOPATH", "/tmp/go"}, {"GOPROXY", "off"}, {"GOSUMDB", "off"}, {"GOTOOLCHAIN", "local"}, {"GOTELEMETRY", "off"}, {"GOMAXPROCS", "2"}, {"CGO_ENABLED", "0"}} {
		args = append(args, "--setenv", entry[0], entry[1])
	}
	args = append(args, "--", goPath, "test", "-json", "-count=1", "-mod=readonly", "-p=2", "-timeout=60s")
	return append(args, cfg.TestPackages...), nil
}

// goToolchainRoot resolves the go binary from PATH (goBinary) and its GOROOT, so the sandbox
// can bind the toolchain that is actually installed instead of assuming /usr/bin/go. It
// returns the binary's path exactly as bwrap must see it (goPath lives under root, since
// GOROOT/bin/go is go's own layout contract) plus root for the --ro-bind call.
func goToolchainRoot(ctx context.Context) (goPath, root string, err error) {
	goPath, err = goBinary()
	if err != nil {
		return "", "", err
	}
	data, err := command(ctx, "", []string{"PATH=" + filepath.Dir(goPath)}, 4096, goPath, "env", "GOROOT")
	if err != nil {
		return "", "", errors.New("cannot discover installed Go toolchain root")
	}
	root = strings.TrimSpace(string(data))
	if !cleanAbsolute(root) {
		return "", "", errors.New("invalid installed Go toolchain root")
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
