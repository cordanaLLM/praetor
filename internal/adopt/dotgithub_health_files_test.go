package adopt

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
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

// TestAdopt_Precedence_DirOther_RemoteDotGitHub_DeclinesAllThree verifies that a greenfield
// adoption in a linked worktree named other with an origin remote pointing to a .github repository
// declines all three health files (origin remote identity takes precedence over directory name).
// Planted negative test: fails on d17698fd8.
func TestAdopt_Precedence_DirOther_RemoteDotGitHub_DeclinesAllThree(t *testing.T) {
	repoPath := newTestRepoCustom(t, "other-worktree", ".github")
	lockSource := newAdoptLockSource(t)

	rep, err := Adopt(context.Background(), AdoptOptions{
		LockSourceRoot: lockSource,
		Path:           repoPath,
		Profile:        "framework",
	})
	if err != nil {
		t.Fatalf("adopt failed: %v", err)
	}
	assertNoHealthFilesPlanned(t, rep)
	assertNoHealthFilesOnDisk(t, repoPath)
	assertHealthFilesImplicitlyDeclined(t, rep)
}

// TestAdopt_Precedence_DirDotGitHub_ManifestNameOther_WritesAllThree verifies that when a checkout
// directory is named .github but the manifest declares repository.name other, the manifest name
// wins and all three health files are written.
// Planted negative test: fails on d17698fd8.
func TestAdopt_Precedence_DirDotGitHub_ManifestNameOther_WritesAllThree(t *testing.T) {
	repoPath := newTestRepoCustom(t, ".github", "other")
	lockSource := newAdoptLockSource(t)
	mustWrite(t, filepath.Join(repoPath, manifestFile), "version: 1\nrepository:\n  owner: acme\n  name: other\nprofiles:\n  - framework\n")

	rep, err := Adopt(context.Background(), AdoptOptions{
		LockSourceRoot: lockSource,
		Path:           repoPath,
	})
	if err != nil {
		t.Fatalf("adopt failed: %v", err)
	}
	_ = rep
	assertAllHealthFilesOnDisk(t, repoPath)
}

// TestAdopt_Precedence_DirOther_ManifestNameDotGitHub_DeclinesAllThree verifies that when a checkout
// directory is named other but the manifest declares repository.name .github, the manifest name
// wins and all three health files are declined.
func TestAdopt_Precedence_DirOther_ManifestNameDotGitHub_DeclinesAllThree(t *testing.T) {
	repoPath := newTestRepoCustom(t, "other-worktree", "other")
	lockSource := newAdoptLockSource(t)
	mustWrite(t, filepath.Join(repoPath, manifestFile), "version: 1\nrepository:\n  owner: acme\n  name: .github\nprofiles:\n  - framework\n")

	rep, err := Adopt(context.Background(), AdoptOptions{
		LockSourceRoot: lockSource,
		Path:           repoPath,
	})
	if err != nil {
		t.Fatalf("adopt failed: %v", err)
	}
	assertNoHealthFilesPlanned(t, rep)
	assertNoHealthFilesOnDisk(t, repoPath)
	assertHealthFilesImplicitlyDeclined(t, rep)
}

// TestAdopt_Precedence_DirDotGitHub_RemoteOther_WritesAllThree verifies that when a checkout
// directory is named .github without a manifest, but the origin remote names other, origin
// remote identity wins over directory name and all three health files are written.
func TestAdopt_Precedence_DirDotGitHub_RemoteOther_WritesAllThree(t *testing.T) {
	repoPath := newTestRepoCustom(t, ".github", "other")
	lockSource := newAdoptLockSource(t)

	rep, err := Adopt(context.Background(), AdoptOptions{
		LockSourceRoot: lockSource,
		Path:           repoPath,
		Profile:        "framework",
	})
	if err != nil {
		t.Fatalf("adopt failed: %v", err)
	}
	_ = rep
	assertAllHealthFilesOnDisk(t, repoPath)
}

// TestAdopt_DotGitHub_CaseVariants verifies that case variants (.GitHub, .GITHUB) in manifest
// repository.name match .github case-insensitively and decline health files.
func TestAdopt_DotGitHub_CaseVariants(t *testing.T) {
	lockSource := newAdoptLockSource(t)
	for _, variant := range []string{".GitHub", ".GITHUB"} {
		repoPath := newTestRepoCustom(t, "worktree", "repo")
		mustWrite(t, filepath.Join(repoPath, manifestFile), "version: 1\nrepository:\n  owner: acme\n  name: "+variant+"\nprofiles:\n  - framework\n")
		rep, err := Adopt(context.Background(), AdoptOptions{
			LockSourceRoot: lockSource,
			Path:           repoPath,
		})
		if err != nil {
			t.Fatalf("adopt %s failed: %v", variant, err)
		}
		assertNoHealthFilesPlanned(t, rep)
		assertNoHealthFilesOnDisk(t, repoPath)
		assertHealthFilesImplicitlyDeclined(t, rep)
	}
}

// TestAdopt_NameContainingGitHub_NoDot_WritesAllThree verifies that names containing github
// without a leading dot (e.g. github, my-github) do not match and write health files.
func TestAdopt_NameContainingGitHub_NoDot_WritesAllThree(t *testing.T) {
	lockSource := newAdoptLockSource(t)
	for _, name := range []string{"github", "my-github"} {
		repoPath := newTestRepoCustom(t, name, name)
		rep, err := Adopt(context.Background(), AdoptOptions{
			LockSourceRoot: lockSource,
			Path:           repoPath,
			Profile:        "framework",
		})
		if err != nil {
			t.Fatalf("adopt %s failed: %v", name, err)
		}
		_ = rep
		assertAllHealthFilesOnDisk(t, repoPath)
	}
}

// TestIsDotGitHubRepository_PrecedenceAndExactMatch tests the one helper across precedence tiers.
func TestIsDotGitHubRepository_PrecedenceAndExactMatch(t *testing.T) {
	tests := []struct {
		name         string
		manifestName string
		identityName string
		repoPath     string
		want         bool
	}{
		{"manifest other wins over remote .github", "other", ".github", "/tmp/.github", false},
		{"manifest .github wins over remote other", ".github", "other", "/tmp/other", true},
		{"manifest case variant .GitHub", ".GitHub", "", "/tmp/other", true},
		{"manifest case variant .GITHUB", ".GITHUB", "", "/tmp/other", true},
		{"remote .github wins over dir other", "", ".github", "/tmp/other", true},
		{"remote other wins over dir .github", "", "other", "/tmp/.github", false},
		{"remote case variant .GitHub", "", ".GitHub", "/tmp/other", true},
		{"dir .github used when others empty", "", "", "/tmp/.github", true},
		{"dir .GitHub used when others empty", "", "", "/tmp/.GitHub", true},
		{"dir other used when others empty", "", "", "/tmp/other", false},
		{"name containing github without dot manifest", "github", "", "", false},
		{"name containing github without dot remote", "", "github", "", false},
		{"name containing github without dot dir", "", "", "/tmp/github", false},
		{"suffix .github-repo no match", ".github-repo", "", "", false},
		{"all empty", "", "", "", false},
		{"windows path dir .github", "", "", `C:\repos\.github`, true},
	}
	for i := 0; i < len(tests); i++ {
		tc := tests[i]
		t.Run(tc.name, func(t *testing.T) {
			var m *config.Manifest
			if tc.manifestName != "" {
				m = &config.Manifest{Repository: config.RepositoryMetadata{Name: tc.manifestName}}
			}
			got := IsDotGitHubRepository(m, tc.identityName, tc.repoPath)
			if got != tc.want {
				t.Fatalf("IsDotGitHubRepository(%q, %q, %q) = %v, want %v", tc.manifestName, tc.identityName, tc.repoPath, got, tc.want)
			}
		})
	}
}

func newTestRepoCustom(t *testing.T, dirName, remoteRepoName string) string {
	t.Helper()
	stubDir := t.TempDir()
	writeStub(t, stubDir, "lefthook", "exit 1\n")
	hermeticPath(t, stubDir)
	repoPath := filepath.Join(t.TempDir(), dirName)
	if err := os.MkdirAll(repoPath, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	initTestGit(t, repoPath)
	writeOriginRemote(t, repoPath, "https://github.com/acme/"+remoteRepoName+".git")
	return repoPath
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
