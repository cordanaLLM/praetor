package operationalsync

import (
	"context"
	"fmt"
	"github.com/cordanaLLM/praetor/internal/util"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const maxGitOutput = 4 << 20

type gitRunner struct {
	ctx    context.Context
	binary string
	env    []string
}

func newGit(ctx context.Context) (*gitRunner, error) {
	binary, err := exec.LookPath("git")
	if err != nil {
		return nil, err
	}
	binary, err = filepath.Abs(binary)
	if err != nil {
		return nil, err
	}
	env := []string{"PATH=" + scrubbedGitPath(binary), "LANG=C.UTF-8",
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + os.DevNull, "GIT_ATTR_NOSYSTEM=1",
		"GIT_TERMINAL_PROMPT=0", "GIT_ALLOW_PROTOCOL=file", "GIT_OPTIONAL_LOCKS=0",
		"GIT_NO_REPLACE_OBJECTS=1", "GIT_NO_LAZY_FETCH=1"}
	return &gitRunner{ctx: ctx, binary: binary, env: env}, nil
}

// run preserves exact blob bytes and bounds output; no caller shell or Git environment is inherited.
func (g *gitRunner) run(ctx context.Context, dir string, args ...string) ([]byte, error) {
	args = append([]string{"-c", "core.fsmonitor=false"}, args...)
	ctx, err := util.WithCommandEnvironment(ctx, g.env)
	if err != nil {
		return nil, err
	}
	out, err := util.RunCommandBytes(ctx, dir, g.binary, maxGitOutput, args...)
	if err != nil {
		return out.Stdout, fmt.Errorf("git %s: %w: %.1024s", args[2], err, out.Stderr)
	}
	return out.Stdout, nil
}

func (g *gitRunner) text(ctx context.Context, dir string, args ...string) (string, error) {
	out, err := g.run(ctx, dir, args...)
	return strings.TrimSpace(string(out)), err
}

func (g *gitRunner) ancestor(ctx context.Context, dir, old, current string) error {
	_, err := g.run(ctx, dir, "merge-base", "--is-ancestor", old, current)
	if err != nil {
		return fmt.Errorf("required reviewed ancestry %s -> %s: %w", old, current, err)
	}
	return nil
}

func (g *gitRunner) blob(ctx context.Context, dir, sha, path string) ([]byte, error) {
	mode, err := g.text(ctx, dir, "ls-tree", sha, "--", path)
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(mode, "100644 blob ") {
		return nil, fmt.Errorf("%s must be a regular tracked 0644 file", path)
	}
	return g.run(ctx, dir, "show", sha+":"+path)
}

// scrubbedGitPath mirrors internal/dogfood.scrubbedPath: the minimal PATH that still
// finds git and its helpers, joined with the platform's own list separator rather than
// a literal colon, and including the POSIX directories only where they exist.
func scrubbedGitPath(binary string) string {
	entries := []string{filepath.Dir(binary)}
	for _, dir := range []string{"/usr/bin", "/bin"} {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			entries = append(entries, dir)
		}
	}
	return strings.Join(entries, string(os.PathListSeparator))
}
