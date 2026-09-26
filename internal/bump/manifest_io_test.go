package bump

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/contextopt"
)

// goModOfSize returns a syntactically plausible go.mod of exactly size bytes.
func goModOfSize(t *testing.T, size int) string {
	t.Helper()
	head := "module example.com/app\n\nrequire example.com/pkg v1.0.0\n"
	if len(head) > size {
		t.Fatalf("requested manifest of %d bytes is smaller than its header", size)
	}
	return head + strings.Repeat("// filler\n", (size-len(head))/10) + strings.Repeat("/", (size-len(head))%10)
}

// TestReadManifestAppliesTheWriterRule pins the read contract to the write contract:
// an ordinary manifest is returned verbatim (positive), a manifest that the write path
// would refuse is refused at read (negative), and the shared cap holds exactly at
// contextopt.MaxSourceBytes and one byte above it (boundary).
func TestReadManifestAppliesTheWriterRule(t *testing.T) {
	t.Run("ordinary-manifest", func(t *testing.T) {
		repo := t.TempDir()
		body := "module example.com/app\n\nrequire example.com/pkg v1.0.0\n"
		writeGoMod(t, repo, body)
		data, err := readManifest(t.Context(), repo, "go.mod")
		if err != nil {
			t.Fatalf("ordinary manifest refused: %v", err)
		}
		if string(data) != body {
			t.Fatalf("manifest content changed: %q", string(data))
		}
	})

	t.Run("exactly-the-cap", func(t *testing.T) {
		repo := t.TempDir()
		body := goModOfSize(t, contextopt.MaxSourceBytes)
		writeGoMod(t, repo, body)
		data, err := readManifest(t.Context(), repo, "go.mod")
		if err != nil {
			t.Fatalf("manifest of exactly %d bytes refused: %v", contextopt.MaxSourceBytes, err)
		}
		if len(data) != contextopt.MaxSourceBytes {
			t.Fatalf("read %d bytes, want %d", len(data), contextopt.MaxSourceBytes)
		}
	})

	t.Run("one-byte-over-the-cap", func(t *testing.T) {
		repo := t.TempDir()
		writeGoMod(t, repo, goModOfSize(t, contextopt.MaxSourceBytes+1))
		if _, err := readManifest(t.Context(), repo, "go.mod"); err == nil {
			t.Fatalf("manifest of %d bytes accepted", contextopt.MaxSourceBytes+1)
		}
	})

	// A relative target keeps the link inside the root, which is the case the read path
	// used to follow while ReplaceSnapshot refused it. An absolute target was already
	// refused, because os.Root treats it as leaving the root.
	t.Run("in-root-symlink", func(t *testing.T) {
		repo := t.TempDir()
		if err := os.WriteFile(filepath.Join(repo, "real.mod"), []byte("module example.com/app\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("real.mod", filepath.Join(repo, "go.mod")); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		if _, err := readManifest(t.Context(), repo, "go.mod"); err == nil {
			t.Fatal("in-root symlinked manifest accepted at read")
		}
	})

	t.Run("escaping-name", func(t *testing.T) {
		repo := t.TempDir()
		if _, err := readManifest(t.Context(), repo, filepath.Join("..", "go.mod")); err == nil {
			t.Fatal("manifest outside the root accepted")
		}
	})

	// The read runs under the caller's context rather than a detached one, so a caller
	// that has already given up gets its cancellation back instead of a completed read.
	t.Run("cancelled-caller", func(t *testing.T) {
		repo := t.TempDir()
		writeGoMod(t, repo, "module example.com/app\n")
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := readManifest(ctx, repo, "go.mod"); !errors.Is(err, context.Canceled) {
			t.Fatalf("read under a cancelled caller context: err = %v, want context.Canceled", err)
		}
	})
}

// TestOversizedManifestIsRefusedBeforeTheEdit proves the bound now applies before the
// update path computes and attempts a replacement, which is the failure the looser read
// rule produced: the manifest was scanned and edited, and only the write refused it.
func TestOversizedManifestIsRefusedBeforeTheEdit(t *testing.T) {
	repo := t.TempDir()
	body := goModOfSize(t, contextopt.MaxSourceBytes+1)
	writeGoMod(t, repo, body)
	candidate := UpgradeCandidate{Package: "example.com/pkg", CurrentVersion: "v1.0.0", TargetVersion: "v1.1.0", ManifestType: "go.mod"}
	err := fallbackGoModEdit(t.Context(), repo, candidate)
	if err == nil {
		t.Fatal("oversized go.mod accepted for update")
	}
	// The refusal must name the read. Before the shared bound it named the replacement,
	// because the manifest was read, parsed and edited and only the write refused it.
	if !strings.Contains(err.Error(), "read manifest go.mod") {
		t.Fatalf("oversized go.mod refused after the edit rather than at the read: %v", err)
	}
	after, err := os.ReadFile(filepath.Join(repo, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != body {
		t.Fatal("refused update still changed go.mod")
	}
}

// TestStaticNodeScanUsesTheManifestReader covers the third rule that scan_node.go used to
// apply: util.ReadConfined placed no bound on package.json at all.
func TestStaticNodeScanUsesTheManifestReader(t *testing.T) {
	t.Run("ordinary-package-json", func(t *testing.T) {
		repo := t.TempDir()
		manifest := `{"dependencies":{"left-pad":"^1.3.0"}}`
		if err := os.WriteFile(filepath.Join(repo, "package.json"), []byte(manifest), 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := scanPackageJSONStatic(t.Context(), repo, ".")
		if err != nil {
			t.Fatalf("ordinary package.json refused: %v", err)
		}
		if len(got) != 1 || got[0].Package != "left-pad" {
			t.Fatalf("static scan returned %v", got)
		}
	})

	t.Run("oversized-package-json", func(t *testing.T) {
		repo := t.TempDir()
		filler := strings.Repeat("x", contextopt.MaxSourceBytes)
		manifest := `{"name":"` + filler + `","dependencies":{"left-pad":"^1.3.0"}}`
		if err := os.WriteFile(filepath.Join(repo, "package.json"), []byte(manifest), 0o600); err != nil {
			t.Fatal(err)
		}
		if got, err := scanPackageJSONStatic(t.Context(), repo, "."); err == nil {
			t.Fatalf("oversized package.json accepted: %v", got)
		}
	})

	t.Run("symlinked-package-json", func(t *testing.T) {
		repo := t.TempDir()
		target := filepath.Join(repo, "real.json")
		if err := os.WriteFile(target, []byte(`{"dependencies":{"left-pad":"^1.3.0"}}`), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, filepath.Join(repo, "package.json")); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		if got, err := scanPackageJSONStatic(t.Context(), repo, "."); err == nil {
			t.Fatalf("symlinked package.json accepted: %v", got)
		}
	})
}
