package paperclip

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

func TestSynthesizeHarness_Negative_NilContext(t *testing.T) {
	if _, err := SynthesizeHarness(nil, t.TempDir()); err == nil { //nolint:staticcheck // exercising the nil-context contract
		t.Fatal("nil context must be rejected")
	}
}

// unresolvableRepo returns a directory whose parent is named dev, so that neither git
// (the context is cancelled) nor the <owner>/<repo> path shape can identify it and the
// basename fallback is exercised.
func unresolvableRepo(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "dev", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestSynthesizeHarness_Boundary_CancelledContextFallsBackToBasename(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	dir := unresolvableRepo(t, "leaf")
	h, err := SynthesizeHarness(ctx, dir)
	if err != nil {
		t.Fatalf("a cancelled context only disables the git lookup: %v", err)
	}
	if h.Platform != "cordanaLLM/leaf" {
		t.Fatalf("expected basename fallback, got %s", h.Platform)
	}
}

func TestWriteHarness_Negative_NilHarness(t *testing.T) {
	if err := WriteHarness(nil, t.TempDir()); err == nil {
		t.Fatal("nil harness must be rejected")
	}
}

func TestWriteHarness_Negative_EscapingPaperclipDirIsRefused(t *testing.T) {
	repo := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(repo, ".paperclip")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	h, err := SynthesizeHarness(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteHarness(h, repo); !errors.Is(err, util.ErrPathEscapesRoot) {
		t.Fatalf("expected ErrPathEscapesRoot, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "harness.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("nothing may be written through the symlinked .paperclip directory")
	}
}

func TestResolvePlatform_Boundary_IncompleteManifestFallsThrough(t *testing.T) {
	dir := unresolvableRepo(t, "repo")
	if err := os.WriteFile(filepath.Join(dir, ".standards.yaml"), []byte("repository:\n  owner: only-owner\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := resolvePlatform(ctx, dir); got != "cordanaLLM/repo" {
		t.Fatalf("incomplete manifest must fall through to the basename, got %s", got)
	}
}
