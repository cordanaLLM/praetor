package lockdown

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/testsupport"
	"github.com/cordanaLLM/praetor/internal/util"
)

// commitFixture returns a hermetic git repository with one commit, the context its git
// commands run under, and its HEAD.
func commitFixture(t *testing.T) (context.Context, string, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not available: %v", err)
	}
	ctx, err := util.WithCommandEnvironment(t.Context(), testsupport.HermeticGitEnv(t))
	if err != nil {
		t.Fatalf("hermetic git environment: %v", err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("fixture\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "--quiet", "-b", "main"},
		{"add", "README.md"},
		{"commit", "--quiet", "-m", "fixture"},
	} {
		if out, err := util.RunGit(ctx, dir, args...); err != nil {
			t.Skipf("git %v failed in sandbox: %v: %s", args, err, out)
		}
	}
	head, err := util.RunGit(ctx, dir, "rev-parse", "HEAD")
	if err != nil {
		t.Fatalf("git rev-parse HEAD: %v: %s", err, head)
	}
	return ctx, dir, head
}

func TestVerifyReceiptCommit_3D(t *testing.T) {
	ctx, dir, head := commitFixture(t)

	// Positive: the receipt attests the checked-out commit.
	if err := VerifyReceiptCommit(ctx, dir, head); err != nil {
		t.Fatalf("receipt for HEAD rejected: %v", err)
	}

	// Negative: a receipt for another commit names both commits.
	other := strings.Repeat("0", len(head))
	err := VerifyReceiptCommit(ctx, dir, other)
	if !errors.Is(err, ErrCommitMismatch) || !strings.Contains(err.Error(), other) || !strings.Contains(err.Error(), "HEAD is "+head) {
		t.Fatalf("mismatched commit must fail with ErrCommitMismatch naming both commits, got %v", err)
	}

	// Negative: outside a repository there is no HEAD to bind to.
	if err := VerifyReceiptCommit(ctx, t.TempDir(), head); err == nil || errors.Is(err, ErrCommitMismatch) ||
		!strings.Contains(err.Error(), "cannot resolve HEAD") {
		t.Fatalf("a directory without HEAD must fail to resolve, got %v", err)
	}

	// Boundary: an empty, abbreviated or padded commit never matches.
	for name, commit := range map[string]string{"empty": "", "abbreviated": head[:12], "padded": " " + head} {
		if err := VerifyReceiptCommit(ctx, dir, commit); !errors.Is(err, ErrCommitMismatch) {
			t.Errorf("%s commit must fail with ErrCommitMismatch, got %v", name, err)
		}
	}

	// Boundary: a cancelled context stops the git query instead of reporting a match.
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := VerifyReceiptCommit(cancelled, dir, head); err == nil {
		t.Fatal("a cancelled context must not verify a receipt")
	}
}
