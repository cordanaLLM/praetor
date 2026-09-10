package release

import (
	"context"
	"testing"

	"github.com/cordanaLLM/standards/internal/changelog"
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
