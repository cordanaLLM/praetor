package repairrun

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/cordanaLLM/praetor/internal/util"
)

const maxLogBytes = 1 << 20

func command(ctx context.Context, dir string, env []string, limit int, executable string, args ...string) ([]byte, error) {
	ctx, err := util.WithCommandEnvironment(ctx, env)
	if err != nil {
		return nil, err
	}
	output, err := util.RunCommandBytes(ctx, dir, executable, limit, args...)
	data := append(output.Stdout, output.Stderr...)
	if len(data) > limit {
		return data[:limit], errors.Join(err, errors.New("combined subprocess output bound exceeded"))
	}
	return data, err
}

// runGit invokes git in the scrubbed environment, resolving the binary from the caller's PATH.
//
// One place knows both the executable and the environment it runs in. Three call sites each
// naming "/usr/bin/git" beside their own gitEnvironment() call was three chances to keep one and
// fix the other (HISS-19).
func runGit(ctx context.Context, dir string, limit int, args ...string) ([]byte, error) {
	gitPath, err := gitBinary()
	if err != nil {
		return nil, err
	}
	return command(ctx, dir, gitEnvironment(gitPath), limit, gitPath, args...)
}

// gitBinary resolves the git executable once, from the caller's PATH.
//
// This package deliberately runs git in a scrubbed environment, and the absolute "/usr/bin/git"
// went with the scrubbed PATH=/usr/bin:/bin. The hardening is right; the literal path is not.
// git lives at /usr/bin/git on a Debian host, /opt/homebrew/bin/git on Apple silicon, and
// C:\Program Files\Git\... on Windows, so the package could not run anywhere else -- twelve
// repairrun cases failed on Windows for that reason alone (#134).
//
// Resolving through the caller's PATH and then handing the child an absolute path keeps both
// properties: the child still gets a minimal environment it cannot escape through PATH, and the
// binary it runs is the one the operator actually has.
func gitBinary() (string, error) {
	return pathBinary("git")
}

// goBinary resolves the go executable once, from the caller's PATH, for the same reason
// gitBinary does: a fixed "/usr/bin/go" is a distro assumption, not a fact. CI toolchains
// installed by actions/setup-go live under a hosted tool cache (e.g.
// /opt/hostedtoolcache/go/<version>/x64/bin/go), not at /usr/bin/go, and a workstation's go
// can live anywhere the operator put it. Resolving through PATH is the one place that has to
// know where go actually is; callers that also need to sandbox it derive GOROOT from this
// same binary rather than guessing a second path.
func goBinary() (string, error) {
	return pathBinary("go")
}

// pathBinary is the one PATH resolution gitBinary and goBinary share (HISS-19): it returns
// the absolute path handed to a child that runs with a scrubbed environment, and names the
// missing tool when the caller's PATH has none.
func pathBinary(name string) (string, error) {
	resolved, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("repairrun requires %s on PATH: %w", name, err)
	}
	return resolved, nil
}

// gitEnvironment returns the scrubbed environment for one git invocation.
//
// The PATH is built from the resolved binary's own directory rather than a fixed pair, so it is
// still minimal -- git sees exactly one directory -- while actually containing git. The null
// device is asked of the platform for the same reason: /dev/null does not exist on Windows, so
// hardcoding it turned a config-suppression flag into a broken path.
func gitEnvironment(gitPath string) []string {
	binDir := filepath.Dir(gitPath)
	null := os.DevNull
	return []string{
		"PATH=" + binDir,
		"LANG=C.UTF-8",
		"HOME=" + nonexistentHome,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=" + null,
		"GIT_ATTR_NOSYSTEM=1",
		"GIT_NO_REPLACE_OBJECTS=1",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ALLOW_PROTOCOL=",
		"GIT_CONFIG_COUNT=2",
		"GIT_CONFIG_KEY_0=core.fsmonitor",
		"GIT_CONFIG_VALUE_0=false",
		"GIT_CONFIG_KEY_1=core.attributesFile",
		"GIT_CONFIG_VALUE_1=" + null,
	}
}

// nonexistentHome is a directory that must not exist, so git cannot read a user configuration.
// It is deliberately absolute on both platform shapes rather than a POSIX-only literal.
var nonexistentHome = filepath.Join(os.TempDir(), "praetor-repairrun-nonexistent-home")
