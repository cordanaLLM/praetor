package adopt

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

// privateIgnoreRepo is a Git work tree holding a private working directory, and the
// .gitignore given as existing unless absent is true.
func privateIgnoreRepo(t *testing.T, name, existing string, absent bool) (root, ignorePath string) {
	t.Helper()
	root = newTestRepo(t, name)
	if err := os.Mkdir(filepath.Join(root, ".workingdir"), 0o700); err != nil {
		t.Fatal(err)
	}
	ignorePath = filepath.Join(root, ".gitignore")
	if !absent {
		mustWrite(t, ignorePath, existing)
	}
	return root, ignorePath
}

func assertLedgerIgnored(t *testing.T, root string) {
	t.Helper()
	for _, private := range []string{".workingdir/STATE.md", ".workingdir/cluster-guide.md"} {
		if _, err := util.RunGit(t.Context(), root, "check-ignore", "--no-index", "--", private); err != nil {
			t.Fatalf("private path %s is not ignored: %v", private, err)
		}
	}
}

// Positive: a repository whose rules do not exclude the working directory gains the
// canonical block, keeps every operator line, and a second call changes nothing.
func TestEnsurePrivateIgnoreWritesTheBlockOnce(t *testing.T) {
	for name, existing := range map[string]string{
		"absent":         "",
		"operator-rules": "# retain exactly\nuser-output/\n",
		"file-opt-in":    ".workingdir/*\n!.workingdir/STATE.md\n",
	} {
		t.Run(name, func(t *testing.T) {
			root, path := privateIgnoreRepo(t, name, existing, name == "absent")
			outcome, err := EnsurePrivateIgnore(t.Context(), root)
			if err != nil || outcome != PrivateIgnoreWritten {
				t.Fatalf("outcome = %q, err = %v; want written", outcome, err)
			}
			got := mustRead(t, path)
			if name == "absent" && got != ManagedGitIgnoreBlock() {
				t.Fatalf("a fresh .gitignore must hold only the managed block, got %q", got)
			}
			for _, line := range strings.Split(existing, "\n") {
				if line != "" && !slices.Contains(managedIgnoreRules, line) && !strings.Contains(got, line) {
					t.Fatalf("operator line %q lost: %q", line, got)
				}
			}
			if !strings.HasSuffix(got, ManagedGitIgnoreBlock()) {
				t.Fatalf("managed block is not the tail: %q", got)
			}
			assertLedgerIgnored(t, root)
			outcome, err = EnsurePrivateIgnore(t.Context(), root)
			if err != nil || outcome != PrivateIgnoreEffective {
				t.Fatalf("second call: outcome = %q, err = %v; want effective", outcome, err)
			}
			if again := mustRead(t, path); again != got {
				t.Fatalf("second call rewrote .gitignore:\n%s\n---\n%s", got, again)
			}
		})
	}
}

// Positive: any rule that already excludes the directory itself is effective, whatever
// its spelling, and the operator's file is left byte for byte alone.
func TestEnsurePrivateIgnoreLeavesAnEffectiveRuleAlone(t *testing.T) {
	for name, existing := range map[string]string{
		"anchored-directory": "/.workingdir/\n",
		"bare-name":          ".workingdir\n",
		"negation-under-dir": "/.workingdir/\n!.workingdir/STATE.md\n",
	} {
		t.Run(name, func(t *testing.T) {
			root, path := privateIgnoreRepo(t, name, existing, false)
			outcome, err := EnsurePrivateIgnore(t.Context(), root)
			if err != nil || outcome != PrivateIgnoreEffective {
				t.Fatalf("outcome = %q, err = %v; want effective", outcome, err)
			}
			if got := mustRead(t, path); got != existing {
				t.Fatalf("effective .gitignore rewritten: %q", got)
			}
		})
	}
}

// Negative: a declined git-ignore step leaves .gitignore to the operator, and every
// input the call cannot act on safely is an error that writes nothing.
func TestEnsurePrivateIgnoreNegative(t *testing.T) {
	t.Run("declined", func(t *testing.T) {
		root, path := privateIgnoreRepo(t, "declined", "", true)
		mustWrite(t, filepath.Join(root, manifestFile), "adoption:\n  decline:\n    - git-ignore\n")
		outcome, err := EnsurePrivateIgnore(t.Context(), root)
		if err != nil || outcome != PrivateIgnoreDeclined {
			t.Fatalf("outcome = %q, err = %v; want declined", outcome, err)
		}
		if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
			t.Fatalf("declined git-ignore still wrote .gitignore: %v", statErr)
		}
	})
	for name, setup := range map[string]func(t *testing.T, root string){
		"unknown-decline": func(t *testing.T, root string) {
			mustWrite(t, filepath.Join(root, manifestFile), "adoption:\n  decline:\n    - no-such-step\n")
		},
		"unterminated-block": func(t *testing.T, root string) {
			mustWrite(t, filepath.Join(root, ".gitignore"), gitIgnoreManagedBegin+"\n/.workingdir2/\n")
		},
		"working-dir-absent": func(t *testing.T, root string) {
			if err := os.Remove(filepath.Join(root, ".workingdir")); err != nil {
				t.Fatal(err)
			}
		},
		"working-dir-is-file": func(t *testing.T, root string) {
			if err := os.Remove(filepath.Join(root, ".workingdir")); err != nil {
				t.Fatal(err)
			}
			mustWrite(t, filepath.Join(root, ".workingdir"), "not a directory\n")
		},
	} {
		t.Run(name, func(t *testing.T) {
			root, path := privateIgnoreRepo(t, name, "", true)
			setup(t, root)
			before, _ := os.ReadFile(path)
			if outcome, err := EnsurePrivateIgnore(t.Context(), root); err == nil || outcome != PrivateIgnoreUnknown {
				t.Fatalf("outcome = %q, err = %v; want an error", outcome, err)
			}
			if after, _ := os.ReadFile(path); string(after) != string(before) {
				t.Fatalf("refused call changed .gitignore: %q -> %q", before, after)
			}
		})
	}
	var nilContext context.Context
	if _, err := EnsurePrivateIgnore(nilContext, t.TempDir()); err == nil {
		t.Fatal("nil context accepted")
	}
}

// Boundary: a directory in no Git work tree has nothing a commit could publish, so the
// call reports that and creates no .gitignore.
func TestEnsurePrivateIgnoreBoundaryOutsideRepository(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".workingdir"), 0o700); err != nil {
		t.Fatal(err)
	}
	if present, err := util.GitWorktreePresent(t.Context(), root); err != nil || present {
		t.Skipf("temporary directory sits inside a Git work tree (%v, %v); the boundary needs one outside", present, err)
	}
	outcome, err := EnsurePrivateIgnore(t.Context(), root)
	if err != nil || outcome != PrivateIgnoreNoRepository {
		t.Fatalf("outcome = %q, err = %v; want no-repository", outcome, err)
	}
	if _, statErr := os.Stat(filepath.Join(root, ".gitignore")); !os.IsNotExist(statErr) {
		t.Fatalf("no-repository call created .gitignore: %v", statErr)
	}
}
