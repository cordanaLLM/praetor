package adopt

import (
	"context"
	"errors"
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

// readOptional returns the file's content, or "<absent>" when it does not exist.
func readOptional(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "<absent>"
	}
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
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
			for line := range strings.SplitSeq(existing, "\n") {
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
			before := readOptional(t, path)
			if outcome, err := EnsurePrivateIgnore(t.Context(), root); err == nil || outcome != PrivateIgnoreUnknown {
				t.Fatalf("outcome = %q, err = %v; want an error", outcome, err)
			}
			if after := readOptional(t, path); after != before {
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

// evidenceIgnoreRepo is a Git work tree with no private working directory, holding the
// .gitignore given as existing unless absent is true.
func evidenceIgnoreRepo(t *testing.T, name, existing string, absent bool) (root, ignorePath string) {
	t.Helper()
	root = newTestRepo(t, name)
	ignorePath = filepath.Join(root, ".gitignore")
	if !absent {
		mustWrite(t, ignorePath, existing)
	}
	return root, ignorePath
}

// Positive: the evidence directory need not exist. A repository whose rules miss it gains the
// canonical block and a notice saying so; an effective rule is left byte for byte alone and
// prints nothing.
func TestEnsureEvidenceIgnore_Positive(t *testing.T) {
	root, path := evidenceIgnoreRepo(t, "evidence-written", "user-output/\n", false)
	var out strings.Builder
	if err := ReconcileEvidenceIgnore(t.Context(), &out, root); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if got := mustRead(t, path); !strings.HasSuffix(got, ManagedGitIgnoreBlock()) || !strings.Contains(got, "user-output/") {
		t.Fatalf("managed block not merged: %q", got)
	}
	if !strings.Contains(out.String(), "Added the Praetor private-artifact block to .gitignore") {
		t.Fatalf("written block not reported: %q", out.String())
	}
	if _, err := os.Stat(filepath.Join(root, ".workingdir")); !os.IsNotExist(err) {
		t.Fatalf("reconciliation created the private directory: %v", err)
	}
	effective, effectivePath := evidenceIgnoreRepo(t, "evidence-effective", "/.workingdir/\n", false)
	outcome, err := EnsureEvidenceIgnore(t.Context(), effective)
	if err != nil || outcome != PrivateIgnoreEffective {
		t.Fatalf("outcome = %q, err = %v; want effective", outcome, err)
	}
	out.Reset()
	if err := ReconcileEvidenceIgnore(t.Context(), &out, effective); err != nil || out.Len() != 0 {
		t.Fatalf("an effective rule must print nothing: %q %v", out.String(), err)
	}
	if got := mustRead(t, effectivePath); got != "/.workingdir/\n" {
		t.Fatalf("effective .gitignore rewritten: %q", got)
	}
}

// Negative: a declined git-ignore step writes nothing and prints the warning state prints, and
// an unmergeable .gitignore is an error that leaves the file alone.
func TestEnsureEvidenceIgnore_Negative(t *testing.T) {
	root, path := evidenceIgnoreRepo(t, "evidence-declined", "", true)
	mustWrite(t, filepath.Join(root, manifestFile), "adoption:\n  decline:\n    - git-ignore\n")
	var out strings.Builder
	if err := ReconcileEvidenceIgnore(t.Context(), &out, root); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("declined git-ignore still wrote .gitignore: %v", err)
	}
	want, warning := PrivateIgnoreNotice(PrivateIgnoreDeclined, root)
	if !warning || out.String() != want+"\n" || !strings.HasPrefix(want, "Warning: Git does not ignore .workingdir/") {
		t.Fatalf("decline warning = %q (warning %v), printed %q", want, warning, out.String())
	}
	unterminated := gitIgnoreManagedBegin + "\n/.workingdir2/\n"
	broken, brokenPath := evidenceIgnoreRepo(t, "evidence-unterminated", unterminated, false)
	if err := ReconcileEvidenceIgnore(t.Context(), &out, broken); err == nil || !strings.Contains(err.Error(), ".workingdir/evidence/") {
		t.Fatalf("an unterminated block must fail naming the directory, got %v", err)
	}
	if got := mustRead(t, brokenPath); got != unterminated {
		t.Fatalf("refused reconciliation changed .gitignore: %q", got)
	}
	var nilContext context.Context
	if _, err := EnsureEvidenceIgnore(nilContext, root); err == nil {
		t.Fatal("nil context accepted")
	}
}

// Boundary: a negation that re-includes the evidence directory is not an effective rule, so the
// block is merged after it; a directory in no work tree needs nothing and gets no .gitignore.
func TestEnsureEvidenceIgnore_Boundary(t *testing.T) {
	root, path := evidenceIgnoreRepo(t, "evidence-reinclude", ".workingdir/*\n!.workingdir/evidence/\n", false)
	outcome, err := EnsureEvidenceIgnore(t.Context(), root)
	if err != nil || outcome != PrivateIgnoreWritten || !strings.HasSuffix(mustRead(t, path), ManagedGitIgnoreBlock()) {
		t.Fatalf("outcome = %q, err = %v; want the block written after the negation", outcome, err)
	}
	outside := t.TempDir()
	if present, err := util.GitWorktreePresent(t.Context(), outside); err != nil || present {
		t.Skipf("temporary directory sits inside a Git work tree (%v, %v)", present, err)
	}
	outcome, err = EnsureEvidenceIgnore(t.Context(), outside)
	if err != nil || outcome != PrivateIgnoreNoRepository {
		t.Fatalf("outcome = %q, err = %v; want no-repository", outcome, err)
	}
	if line, warning := PrivateIgnoreNotice(outcome, outside); line != "" || warning {
		t.Fatalf("no-repository must print nothing: %q", line)
	}
	if _, statErr := os.Stat(filepath.Join(outside, ".gitignore")); !os.IsNotExist(statErr) {
		t.Fatalf("no-repository call created .gitignore: %v", statErr)
	}
}
