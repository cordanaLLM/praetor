package release

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/changelog"
	"github.com/cordanaLLM/praetor/internal/semver"
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
		if _, ok := semver.Parse(v); !ok {
			t.Errorf("expected %q to be valid SemVer", v)
		}
	}

	invalid := []string{"1.0", "v1", "1.2.3.4", "", "latest", "v1.2.3-"}
	for _, v := range invalid {
		if _, ok := semver.Parse(v); ok {
			t.Errorf("expected %q to be invalid SemVer", v)
		}
	}
}

// Negative + boundary: with no fragment to render, PrepareRelease fails instead of reporting
// success over an unchanged CHANGELOG.md, and the message tells a missing changelog.d apart
// from an empty one. Both wrap changelog.ErrNoFragments.
func TestPrepareRelease_Negative_NoFragments(t *testing.T) {
	cases := map[string]struct {
		mkdir bool
		want  string
	}{
		"absent fragment directory": {mkdir: false, want: "changelog.d does not exist"},
		"empty fragment directory":  {mkdir: true, want: "changelog.d holds none"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			tmpDir := t.TempDir()
			if tc.mkdir {
				if err := os.Mkdir(filepath.Join(tmpDir, "changelog.d"), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			opts := ReleaseOptions{RepoPath: tmpDir, Version: "v1.2.0", Date: "2026-09-11", SkipVerify: true, SkipClean: true}
			err := PrepareRelease(context.Background(), opts)
			if !errors.Is(err, changelog.ErrNoFragments) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %v: want ErrNoFragments naming %q", err, tc.want)
			}
			if _, statErr := os.Stat(filepath.Join(tmpDir, "CHANGELOG.md")); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("a refused render must not write CHANGELOG.md: %v", statErr)
			}
		})
	}
}

// Boundary: the first release consumes the only fragment; preparing the next version with
// nothing new fails rather than silently publishing nothing.
func TestPrepareRelease_Boundary_SecondReleaseWithoutNewFragments(t *testing.T) {
	tmpDir := t.TempDir()
	if _, err := changelog.CreateFragment(tmpDir, changelog.Fragment{Type: changelog.TypeFixed, Title: "Only fix"}); err != nil {
		t.Fatal(err)
	}
	opts := ReleaseOptions{RepoPath: tmpDir, Version: "v1.2.0", Date: "2026-09-11", SkipVerify: true, SkipClean: true}
	if err := PrepareRelease(context.Background(), opts); err != nil {
		t.Fatalf("first release: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(tmpDir, "CHANGELOG.md"))
	if err != nil || !strings.Contains(string(content), "Only fix") {
		t.Fatalf("first release must render its fragment: %q, %v", content, err)
	}
	opts.Version = "v1.2.1"
	if err := PrepareRelease(context.Background(), opts); !errors.Is(err, changelog.ErrNoFragments) {
		t.Fatalf("second release without fragments: got %v, want ErrNoFragments", err)
	}
}
