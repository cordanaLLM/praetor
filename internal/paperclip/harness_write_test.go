package paperclip

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

func synthesizedHarness(t *testing.T, repo string) *Harness {
	t.Helper()
	h, _, err := SynthesizeHarness(context.Background(), repo, unknownFacts)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// Positive: both harness files land in .paperclip below the repository.
func TestWriteHarnessFiles_Positive_BothFiles(t *testing.T) {
	repo := identifiedRepo(t)
	if err := WriteHarnessFiles(synthesizedHarness(t, repo), repo, true); err != nil {
		t.Fatalf("WriteHarnessFiles: %v", err)
	}
	for _, name := range []string{harnessFile, rulesFile} {
		if info, err := os.Lstat(filepath.Join(repo, paperclipDir, name)); err != nil || !info.Mode().IsRegular() {
			t.Fatalf("%s = %v, %v; want a regular file", name, info, err)
		}
	}
}

// Negative: a link planted at rules.md that stays inside the repository is refused instead
// of written through, and its target keeps its content (BUG-826).
func TestWriteHarnessFiles_Negative_LinkedRulesFile(t *testing.T) {
	repo := identifiedRepo(t)
	target := filepath.Join(repo, "README.md")
	if err := os.WriteFile(target, []byte("kept\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, paperclipDir), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("..", "README.md"), filepath.Join(repo, paperclipDir, rulesFile)); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := WriteHarnessFiles(synthesizedHarness(t, repo), repo, true); !errors.Is(err, util.ErrSymlinkDestination) {
		t.Fatalf("linked rules.md = %v, want ErrSymlinkDestination", err)
	}
	if data, err := os.ReadFile(target); err != nil || string(data) != "kept\n" { // #nosec G304 -- test-local path
		t.Fatalf("link target = %q, %v; want it untouched", data, err)
	}
}

// Boundary: a .paperclip that is a relative link staying inside the repository is followed,
// so the confinement edge is the repository root, not every link below it.
func TestWriteHarnessFiles_Boundary_InRootLinkedDirectory(t *testing.T) {
	repo := identifiedRepo(t)
	if err := os.MkdirAll(filepath.Join(repo, "state", "paperclip"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("state", "paperclip"), filepath.Join(repo, paperclipDir)); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := WriteHarnessFiles(synthesizedHarness(t, repo), repo, false); err != nil {
		t.Fatalf("in-repository linked .paperclip = %v, want the write to follow it", err)
	}
	if _, err := os.Stat(filepath.Join(repo, "state", "paperclip", harnessFile)); err != nil {
		t.Fatalf("harness.json not written through the in-repository link: %v", err)
	}
}
