package adopt

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// TestAdopt_DotGitHub_HealthFiles_WritesNoneAndPlansNone verifies that adopt on a
// repository named .github writes none of contributing, pull-request-template, and
// security-policy, and plans none in dry run. Planted test; red on base.
func TestAdopt_DotGitHub_HealthFiles_WritesNoneAndPlansNone(t *testing.T) {
	repoPath := newTestRepo(t, ".github")
	lockSource := newAdoptLockSource(t)

	// Dry run on .github repository: plans none of the three files.
	repDry, err := Adopt(context.Background(), AdoptOptions{
		LockSourceRoot: lockSource,
		Path:           repoPath,
		Profile:        "framework",
		DryRun:         true,
	})
	if err != nil {
		t.Fatalf("dry run adopt failed: %v", err)
	}
	assertNoHealthFilesPlanned(t, repDry)
	assertNoHealthFilesOnDisk(t, repoPath)

	// Real adopt on .github repository: writes none of the three files.
	rep, err := Adopt(context.Background(), AdoptOptions{
		LockSourceRoot: lockSource,
		Path:           repoPath,
		Profile:        "framework",
	})
	if err != nil {
		t.Fatalf("real adopt failed: %v", err)
	}
	assertNoHealthFilesPlanned(t, rep)
	assertNoHealthFilesOnDisk(t, repoPath)
	assertHealthFilesImplicitlyDeclined(t, rep)
}

// TestAdopt_OtherRepo_HealthFiles_WritesAllThree verifies that the same flow on a
// repository named other writes all three community health files. Planted test.
func TestAdopt_OtherRepo_HealthFiles_WritesAllThree(t *testing.T) {
	repoPath := newTestRepo(t, "other")
	lockSource := newAdoptLockSource(t)

	// Dry run on other repository: plans all three files.
	repDry, err := Adopt(context.Background(), AdoptOptions{
		LockSourceRoot: lockSource,
		Path:           repoPath,
		Profile:        "framework",
		DryRun:         true,
	})
	if err != nil {
		t.Fatalf("dry run adopt failed: %v", err)
	}
	assertAllHealthFilesPlanned(t, repDry)
	assertNoHealthFilesOnDisk(t, repoPath)

	// Real adopt on other repository: writes all three files.
	rep, err := Adopt(context.Background(), AdoptOptions{
		LockSourceRoot: lockSource,
		Path:           repoPath,
		Profile:        "framework",
	})
	if err != nil {
		t.Fatalf("real adopt failed: %v", err)
	}
	_ = rep
	assertAllHealthFilesOnDisk(t, repoPath)
}

// TestAdopt_DotGitHub_ExistingContributing_ByteIdenticalUnderForce verifies that an
// existing CONTRIBUTING.md in a .github repository is never modified, --force included.
// Planted test; red on base.
func TestAdopt_DotGitHub_ExistingContributing_ByteIdenticalUnderForce(t *testing.T) {
	repoPath := newTestRepo(t, ".github")
	lockSource := newAdoptLockSource(t)
	const customGuide = "# Custom Organisation Contributor Guide\nPreserve this byte-identical.\n"
	mustWrite(t, filepath.Join(repoPath, contributingFile), customGuide)

	rep, err := Adopt(context.Background(), AdoptOptions{
		LockSourceRoot: lockSource,
		Path:           repoPath,
		Profile:        "framework",
		Force:          true,
	})
	if err != nil {
		t.Fatalf("adopt --force failed: %v", err)
	}

	got := mustRead(t, filepath.Join(repoPath, contributingFile))
	if got != customGuide {
		t.Fatalf("existing CONTRIBUTING.md modified under --force: got %q, want %q", got, customGuide)
	}
	for i := 0; i < len(rep.Warnings) && i < maxReportActions; i++ {
		if strings.Contains(rep.Warnings[i], contributingFile) && strings.Contains(rep.Warnings[i], "differs from the scaffold") {
			t.Errorf("unexpected drift warning for %s: %s", contributingFile, rep.Warnings[i])
		}
	}
}

func assertNoHealthFilesPlanned(t *testing.T, rep *AdoptReport) {
	t.Helper()
	healthFiles := []string{contributingFile, prTemplateFile, securityFile}
	for i := 0; i < len(healthFiles) && i < maxDeclinedArtifacts; i++ {
		rel := healthFiles[i]
		for j := 0; j < len(rep.CreatedFiles) && j < maxReportActions; j++ {
			if rep.CreatedFiles[j] == rel {
				t.Errorf(".github repository must not plan or create %s", rel)
			}
		}
	}
}

func assertNoHealthFilesOnDisk(t *testing.T, repoPath string) {
	t.Helper()
	healthFiles := []string{contributingFile, prTemplateFile, securityFile}
	for i := 0; i < len(healthFiles) && i < maxDeclinedArtifacts; i++ {
		rel := healthFiles[i]
		if fileExists(filepath.Join(repoPath, rel)) {
			t.Errorf(".github repository must not write %s to disk", rel)
		}
	}
}

func assertHealthFilesImplicitlyDeclined(t *testing.T, rep *AdoptReport) {
	t.Helper()
	steps := []string{"contributing", "pull-request-template", "security-policy"}
	for i := 0; i < len(steps) && i < maxDeclinedArtifacts; i++ {
		step := steps[i]
		found := false
		for j := 0; j < len(rep.ActionDetails) && j < maxReportActions; j++ {
			d := rep.ActionDetails[j]
			if d.Path == step && d.Action == actionSkip && strings.Contains(d.Details, "organisation community health files") {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("step %s was not reported as implicitly declined with reason 'organisation community health files'", step)
		}
	}
}

func assertAllHealthFilesPlanned(t *testing.T, rep *AdoptReport) {
	t.Helper()
	healthFiles := []string{contributingFile, prTemplateFile, securityFile}
	for i := 0; i < len(healthFiles) && i < maxDeclinedArtifacts; i++ {
		rel := healthFiles[i]
		found := false
		for j := 0; j < len(rep.CreatedFiles) && j < maxReportActions; j++ {
			if rep.CreatedFiles[j] == rel {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("other repository must plan %s", rel)
		}
	}
}

func assertAllHealthFilesOnDisk(t *testing.T, repoPath string) {
	t.Helper()
	healthFiles := []string{contributingFile, prTemplateFile, securityFile}
	for i := 0; i < len(healthFiles) && i < maxDeclinedArtifacts; i++ {
		rel := healthFiles[i]
		if !fileExists(filepath.Join(repoPath, rel)) {
			t.Errorf("other repository must write %s to disk", rel)
		}
	}
}
