package release

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/cordanaLLM/praetor/internal/changelog"
)

func TestPrepareRelease_Positive(t *testing.T) {
	tmpDir := t.TempDir()

	// Seed fragment
	_, err := changelog.CreateFragment(tmpDir, changelog.Fragment{
		Type:  changelog.TypeAdded,
		Title: "Test feature for release",
	})
	if err != nil {
		t.Fatal(err)
	}

	opts := ReleaseOptions{
		RepoPath:   tmpDir,
		Version:    "v1.2.0",
		Date:       "2026-09-11",
		SkipVerify: true,
		SkipClean:  true,
	}

	err = PrepareRelease(context.Background(), opts)
	if err != nil {
		t.Fatalf("PrepareRelease failed: %v", err)
	}
}

func TestPrepareRelease_Negative_InvalidSemVer(t *testing.T) {
	tmpDir := t.TempDir()
	opts := ReleaseOptions{
		RepoPath:   tmpDir,
		Version:    "not-a-version",
		SkipVerify: true,
		SkipClean:  true,
	}

	err := PrepareRelease(context.Background(), opts)
	if err == nil {
		t.Fatal("expected error with invalid semver")
	}
}

func TestPrepareRelease_Negative_DirtyWorkingTree(t *testing.T) {
	tmpDir := t.TempDir()
	// Init git repo
	cmd := exec.Command("git", "init")
	cmd.Dir = tmpDir
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}

	// Create dirty uncommitted file
	if err := os.WriteFile(filepath.Join(tmpDir, "dirty.txt"), []byte("dirty"), 0644); err != nil {
		t.Fatal(err)
	}

	opts := ReleaseOptions{
		RepoPath:   tmpDir,
		Version:    "v1.0.0",
		SkipVerify: true,
		SkipClean:  false,
	}

	err := PrepareRelease(context.Background(), opts)
	if err == nil {
		t.Fatal("expected error when working tree is dirty")
	}
}

func TestPrepareRelease_Negative_VerifyFailed(t *testing.T) {
	tmpDir := t.TempDir()
	opts := ReleaseOptions{
		RepoPath:   tmpDir,
		Version:    "1.0.0",
		SkipVerify: false,
		SkipClean:  true,
	}

	// Make verify-all will fail because Makefile is absent
	err := PrepareRelease(context.Background(), opts)
	if err == nil {
		t.Fatal("expected error when make verify-all fails")
	}
}

func TestPrepareRelease_Boundary_SemverPatterns(t *testing.T) {
	valid := []string{"1.0.0", "v1.2.3", "0.0.1-rc.1", "2.1.0+build.42", "v3.0.0-beta.2+exp.sha.5114f85"}
	for _, v := range valid {
		if !semverRegex.MatchString(v) {
			t.Errorf("expected %q to be valid SemVer", v)
		}
	}

	invalid := []string{"1.0", "v1", "1.2.3.4", "", "latest", "v1.2.3-"}
	for _, v := range invalid {
		if semverRegex.MatchString(v) {
			t.Errorf("expected %q to be invalid SemVer", v)
		}
	}
}
