package bump

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/testsupport"
)

const readmeAdaptation = "diff --git a/README.md b/README.md\n--- a/README.md\n+++ b/README.md\n@@ -1 +1 @@\n-before\n+after\n"

// replacingPnpm performs the dependency update in the working directory and then overwrites
// the file named by PRAETOR_TEST_PATCH, so a caller that re-reads the patch sees the change.
const replacingPnpm = `package main

import "os"

func main() {
	if err := os.WriteFile("package.json", []byte("{\"dependencies\":{\"fixture-dep\":\"^2.0.0\"}}\n"), 0o600); err != nil {
		os.Exit(1)
	}
	if err := os.WriteFile(os.Getenv("PRAETOR_TEST_PATCH"), []byte("replacement is not a patch\n"), 0o600); err != nil {
		os.Exit(1)
	}
}
`

func TestApplyBumpMissingPatchFailsBeforeUpdate(t *testing.T) {
	dir, candidate := canaryFixture(t)
	before, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	err = ApplyBump(t.Context(), dir, candidate, filepath.Join(dir, "missing.patch"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("requested missing patch silently skipped: %v", err)
	}
	assertCanaryFile(t, filepath.Join(dir, "package.json"), string(before))
}

func TestApplyBumpRejectsInvalidPatchBeforeUpdate(t *testing.T) {
	for _, kind := range []string{"diagnostic", "oversized", "linked", "directory", "empty"} {
		t.Run(kind, func(t *testing.T) {
			dir, candidate := canaryFixture(t)
			patch := filepath.Join(dir, "candidate.patch")
			body := "# failure diagnostics are not a diff\n"
			switch kind {
			case "oversized":
				body = strings.Repeat("x", (1<<20)+1)
			case "empty":
				body = ""
			case "linked":
				if err := os.Symlink("README.md", patch); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Mkdir(patch, 0700); err != nil {
					t.Fatal(err)
				}
			}
			if kind != "linked" && kind != "directory" {
				writeCanaryFile(t, patch, body)
			}
			if err := ApplyBump(t.Context(), dir, candidate, patch); !errors.Is(err, ErrInvalidPatch) {
				t.Fatalf("invalid patch accepted: %v", err)
			}
			assertCanaryFile(t, filepath.Join(dir, "package.json"), `{"dependencies":{"fixture-dep":"^1.0.0"}}`+"\n")
		})
	}
}

func TestApplyBumpAllowsPostUpdateManifestPatch(t *testing.T) {
	dir, candidate := canaryFixture(t)
	patch := filepath.Join(dir, "post-update.patch")
	// The update rewrites only the range, so the patch is written against the fixture's
	// own single-line layout.
	writeCanaryFile(t, patch, "diff --git a/package.json b/package.json\n--- a/package.json\n+++ b/package.json\n@@ -1 +1,2 @@\n-{\"dependencies\":{\"fixture-dep\":\"^2.0.0\"}}\n+{\"adapted\":true,\n+\"dependencies\":{\"fixture-dep\":\"^2.0.0\"}}\n")
	if err := ApplyBump(t.Context(), dir, candidate, patch); err != nil {
		t.Fatal(err)
	}
	assertCanaryFile(t, filepath.Join(dir, "package.json"), "{\"adapted\":true,\n\"dependencies\":{\"fixture-dep\":\"^2.0.0\"}}\n")
}

func TestApplyBumpUsesRetainedPatchBytes(t *testing.T) {
	dir, candidate := canaryFixture(t)
	patch := filepath.Join(dir, "original.patch")
	writeCanaryFile(t, patch, readmeAdaptation)
	writeCanaryFile(t, filepath.Join(dir, "pnpm-lock.yaml"), "fixture\n")
	bin := t.TempDir()
	testsupport.BuildExecutable(t, bin, "pnpm", replacingPnpm)
	t.Setenv("PRAETOR_TEST_PATCH", patch)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	if err := ApplyBump(t.Context(), dir, candidate, patch); err != nil {
		t.Fatal(err)
	}
	assertCanaryFile(t, patch, "replacement is not a patch\n")
	assertCanaryFile(t, filepath.Join(dir, "README.md"), "after\n")
}

func TestApplyBumpReportsPostUpdateApplicabilityFailure(t *testing.T) {
	dir, candidate := canaryFixture(t)
	patch := filepath.Join(dir, "conflict.patch")
	writeCanaryFile(t, patch, strings.Replace(readmeAdaptation, "-before", "-not present", 1))
	err := ApplyBump(t.Context(), dir, candidate, patch)
	if err == nil || !strings.Contains(err.Error(), "dependency update applied") {
		t.Fatalf("partial update not reported: %v", err)
	}
	assertCanaryFile(t, filepath.Join(dir, "README.md"), "before\n")
	body, readErr := os.ReadFile(filepath.Join(dir, "package.json"))
	if readErr != nil || !strings.Contains(string(body), "2.0.0") {
		t.Fatalf("test did not reach post-update failure: %s, %v", body, readErr)
	}
}

func writeCanaryFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func assertCanaryFile(t *testing.T, path, expected string) {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil || string(body) != expected {
		t.Fatalf("unexpected %s content: %q, %v", path, body, err)
	}
}
