// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package workstation

import (
	"context"
	"os"
	"os/exec"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

// fakeCheckout creates a real, minimal git repository with one commit, so engineCommit
// (git rev-parse HEAD) has something to resolve. Every test in this package builds from a
// temp directory only; none of it ever runs against the module's own checkout.
func fakeCheckout(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH; workstation install cannot resolve a checkout commit on this leg")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		if _, err := util.RunGit(context.Background(), dir, args...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	run("init", "-q")
	run("-c", "user.email=test@example.com", "-c", "user.name=test", "commit", "--allow-empty", "-q", "-m", "init")
	return dir
}

// fakeBuild returns a BuildFunc that writes deterministic, distinguishable bytes instead of
// invoking the real Go compiler, so tests exercise Install's lock, backup, swap and
// manifest logic without paying for a compiler invocation per run.
func fakeBuild(tag string) BuildFunc {
	return func(_ context.Context, _, name, destination string) error {
		return os.WriteFile(destination, []byte(tag+":"+name), 0o755)
	}
}

// failingBuild fails only for name, so a placement earlier in binaryNames order still
// completes and Install's rollback has something to undo.
func failingBuild(name string, cause error) BuildFunc {
	return func(_ context.Context, _, candidate, destination string) error {
		if candidate == name {
			return cause
		}
		return os.WriteFile(destination, []byte("ok:"+candidate), 0o755)
	}
}
