package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/devcontainer"
	"github.com/cordanaLLM/praetor/internal/util"
)

// TestResolveLockSourceRoot_3D tests the lock source root resolver directly across
// positive, negative, and boundary inputs (HISS-15).
// Positive: a checkout, a checkout subdirectory and a plain directory each return as given.
// Negative: a missing path, a regular file and a blank value each fail naming the flag, the
// quoted path and the cause (a blank value is no silent "no source", rule 15).
// Boundary: an unset (empty) value returns ("", nil).
func TestResolveLockSourceRoot_3D(t *testing.T) {
	source, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}

	got, err := resolveLockSourceRoot("", "--lock-source-root")
	if err != nil || got != "" {
		t.Fatalf("resolveLockSourceRoot(\"\"): want empty string and nil error, got %q, %v", got, err)
	}

	for _, dir := range []string{source, filepath.Join(source, "cmd", "standardsctl"), t.TempDir()} {
		got, err := resolveLockSourceRoot(dir, "--lock-source-root")
		if err != nil || got != dir {
			t.Fatalf("resolveLockSourceRoot(%q) must keep the path as given: got %q, %v", dir, got, err)
		}
	}

	testResolveLockSourceRootNegatives(t)
}

func testResolveLockSourceRootNegatives(t *testing.T) {
	t.Helper()

	filePath := filepath.Join(t.TempDir(), "plain-file.txt")
	if err := os.WriteFile(filePath, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, refused := range []struct{ value, cause string }{
		{filepath.Join(t.TempDir(), "absent-dir"), "missing"},
		{filePath, "not a directory"},
		{"   ", "blank"},
		{"\t\n", "blank"},
	} {
		got, err := resolveLockSourceRoot(refused.value, "--lock-source-root")
		if err == nil || got != "" {
			t.Fatalf("resolveLockSourceRoot(%q) = %q, %v; want a refusal", refused.value, got, err)
		}
		for _, phrase := range []string{"--lock-source-root", quoted(refused.value), refused.cause} {
			if !strings.Contains(err.Error(), phrase) {
				t.Fatalf("resolveLockSourceRoot(%q) error lacks %q:\n%v", refused.value, phrase, err)
			}
		}
	}
}

// TestAdoptLockSourceRootRefusals_3D tests the CLI adoption behavior with invalid and valid
// --lock-source-root flags (HISS-15).
// Positive: a real checkout passes adoption.
// Negative: a source with go.mod outside any Git checkout, a missing path, a file and a blank
// value each fail naming the flag, the quoted path and the cause.
// Boundary: a bundle without go.mod and a subdirectory bundle each pass adoption.
func TestAdoptLockSourceRootRefusals_3D(t *testing.T) {
	t.Setenv("LC_ALL", "C")
	t.Setenv("LANG", "C")
	source, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	initGitFixture(t, root)

	if err := runAdopt([]string{"--path", root, "--dry-run", "--lock-source-root", source}); err != nil {
		t.Fatalf("adopt with real checkout must succeed: %v", err)
	}

	nonGitNoGoMod := newNonGitBundleWithoutGoMod(t, source)
	targetDir1 := t.TempDir()
	initGitFixture(t, targetDir1)
	if err := runAdopt([]string{"--path", targetDir1, "--dry-run", "--lock-source-root", nonGitNoGoMod}); err != nil {
		t.Fatalf("adopt with non-go.mod bundle must succeed: %v", err)
	}

	subDirBundle := newSubdirectoryBundle(t, source)
	targetDir2 := t.TempDir()
	initGitFixture(t, targetDir2)
	if err := runAdopt([]string{"--path", targetDir2, "--dry-run", "--lock-source-root", subDirBundle}); err != nil {
		t.Fatalf("adopt with subdirectory bundle %q must succeed: %v", subDirBundle, err)
	}

	testAdoptLockSourceRootCLINegatives(t, root, source)
}

func testAdoptLockSourceRootCLINegatives(t *testing.T, root, source string) {
	t.Helper()

	nonGitWithGoMod := newNonGitBundleWithGoMod(t, source)
	err := runAdopt([]string{"--path", root, "--dry-run", "--lock-source-root", nonGitWithGoMod})
	if !errors.Is(err, devcontainer.ErrSourceNotGitCheckout) {
		t.Fatalf("adopt with non-git bundle containing go.mod = %v, want ErrSourceNotGitCheckout", err)
	}
	if !strings.Contains(err.Error(), nonGitSourceRefusal("--lock-source-root", nonGitWithGoMod)) {
		t.Fatalf("non-git bundle adopt error lacks the flag, path and cause:\n%v", err)
	}

	err = runAdopt([]string{"--path", root, "--dry-run", "--lock-source-root", "  "})
	if err == nil || !strings.Contains(err.Error(), `--lock-source-root "  ": blank`) {
		t.Fatalf("adopt with a blank --lock-source-root = %v, want a refusal naming the flag", err)
	}

	missingPath := filepath.Join(t.TempDir(), "missing-lock-source")
	err = runAdopt([]string{"--path", root, "--dry-run", "--lock-source-root", missingPath})
	if err == nil {
		t.Fatalf("adopt with missing path must fail")
	}
	for _, phrase := range []string{"--lock-source-root", quoted(missingPath), "missing"} {
		if !strings.Contains(err.Error(), phrase) {
			t.Fatalf("missing path adopt error lacks %q:\n%v", phrase, err)
		}
	}

	filePath := filepath.Join(t.TempDir(), "not-a-dir.txt")
	if err := os.WriteFile(filePath, []byte("sample"), 0o644); err != nil {
		t.Fatal(err)
	}
	err = runAdopt([]string{"--path", root, "--dry-run", "--lock-source-root", filePath})
	if err == nil {
		t.Fatalf("adopt with regular file must fail")
	}
	for _, phrase := range []string{"--lock-source-root", quoted(filePath), "not a directory"} {
		if !strings.Contains(err.Error(), phrase) {
			t.Fatalf("regular file adopt error lacks %q:\n%v", phrase, err)
		}
	}
}

// TestProfileSetLockSourceRoot_3D tests praetorctl profile set with valid and invalid
// --lock-source-root flags (HISS-15).
func TestProfileSetLockSourceRoot_3D(t *testing.T) {
	source, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	target := t.TempDir()
	initGitFixture(t, target)
	if err := runAdopt([]string{"--path", target, "--lock-source-root", source}); err != nil {
		t.Fatalf("adopt target fixture: %v", err)
	}

	if err := runProfileSet([]string{"os-image", "--path", target, "--dry-run", "--lock-source-root", source}); err != nil {
		t.Fatalf("profile set with real checkout must succeed: %v", err)
	}

	nonGitBundle := newNonGitBundleWithoutGoMod(t, source)
	if err := runProfileSet([]string{"os-image", "--path", target, "--dry-run", "--lock-source-root", nonGitBundle}); err != nil {
		t.Fatalf("profile set with non-checkout bundle must succeed: %v", err)
	}

	subDirBundle := newSubdirectoryBundle(t, source)
	if err := runProfileSet([]string{"os-image", "--path", target, "--dry-run", "--lock-source-root", subDirBundle}); err != nil {
		t.Fatalf("profile set with subdirectory bundle must succeed: %v", err)
	}

	testProfileSetLockSourceRootNegatives(t, target)
}

func testProfileSetLockSourceRootNegatives(t *testing.T, target string) {
	t.Helper()

	missingPath := filepath.Join(t.TempDir(), "missing-path")
	err := runProfileSet([]string{"os-image", "--path", target, "--dry-run", "--lock-source-root", missingPath})
	if err == nil {
		t.Fatalf("profile set with missing path must fail")
	}
	for _, phrase := range []string{"--lock-source-root", quoted(missingPath), "missing"} {
		if !strings.Contains(err.Error(), phrase) {
			t.Fatalf("missing path profile set error lacks %q:\n%v", phrase, err)
		}
	}

	filePath := filepath.Join(t.TempDir(), "not-a-dir.txt")
	if err := os.WriteFile(filePath, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	err = runProfileSet([]string{"os-image", "--path", target, "--dry-run", "--lock-source-root", filePath})
	if err == nil {
		t.Fatalf("profile set with regular file must fail")
	}
	for _, phrase := range []string{"--lock-source-root", quoted(filePath), "not a directory"} {
		if !strings.Contains(err.Error(), phrase) {
			t.Fatalf("regular file profile set error lacks %q:\n%v", phrase, err)
		}
	}
}

// TestAdoptLockSourceRootProcessCLI proves the praetorctl process returns exit code 1
// and prints the diagnostic naming the flag, quoted path, and cause to stderr/stdout.
func TestAdoptLockSourceRootProcessCLI(t *testing.T) {
	t.Setenv("LC_ALL", "C")
	t.Setenv("LANG", "C")
	source, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	initGitFixture(t, root)

	nonGitWithGoMod := newNonGitBundleWithGoMod(t, source)
	code, out := praetorctl(t, "adopt", "--path", root, "--dry-run", "--lock-source-root", nonGitWithGoMod)
	if code == 0 {
		t.Fatalf("praetorctl adopt with plain dir must exit non-zero: %d\n%s", code, out)
	}
	if !strings.Contains(out, nonGitSourceRefusal("--lock-source-root", nonGitWithGoMod)) {
		t.Fatalf("CLI output lacks the flag, path and cause:\n%s", out)
	}
}

// nonGitSourceRefusal is the refusal of a source root holding go.mod outside any Git checkout,
// selected through flag: the flag, the step's context, the quoted path and the cause.
func nonGitSourceRefusal(flag, source string) string {
	return flag + ": prepare devcontainer bootstrap: bootstrap source " + quoted(source) +
		": not a Git checkout; the bootstrap inventories it with git ls-files"
}

func copyTestBundle(t *testing.T, src, dst string) {
	t.Helper()
	for _, file := range []string{".standards.yaml", ".standards.lock"} {
		copyTestFile(t, filepath.Join(src, file), filepath.Join(dst, file))
	}
	for _, dir := range []string{
		filepath.Join(".config", "archetypes"),
		filepath.Join(".config", "agent"),
		filepath.Join(".config", "lefthook"),
		filepath.Join(".agents", "skills"),
		"templates",
	} {
		copyTestTree(t, filepath.Join(src, dir), filepath.Join(dst, dir))
	}
}

func copyTestFile(t *testing.T, src, dst string) {
	t.Helper()
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func copyTestTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func newNonGitBundleWithGoMod(t *testing.T, source string) string {
	t.Helper()
	dir := t.TempDir()
	if present, err := util.GitWorktreePresent(t.Context(), dir); err != nil || present {
		t.Skipf("temporary directory sits inside a Git work tree (%v, %v); the refusal needs one outside", present, err)
	}
	copyTestBundle(t, source, dir)
	for _, file := range []string{"go.mod", "go.sum", "LICENSE"} {
		copyTestFile(t, filepath.Join(source, file), filepath.Join(dir, file))
	}
	copyTestFile(t, filepath.Join(source, "cmd", "standardsctl", "main.go"), filepath.Join(dir, "cmd", "standardsctl", "main.go"))
	return dir
}

func newNonGitBundleWithoutGoMod(t *testing.T, source string) string {
	t.Helper()
	dir := t.TempDir()
	copyTestBundle(t, source, dir)
	return dir
}

func newSubdirectoryBundle(t *testing.T, source string) string {
	t.Helper()
	outer := t.TempDir()
	initGitFixture(t, outer)
	sub := filepath.Join(outer, "third_party", "praetor")
	copyTestBundle(t, source, sub)
	return sub
}
