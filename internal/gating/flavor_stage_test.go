package gating

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/flavor"
)

// osImageManifest declares the os-image profile, which the os-image flavor implements.
const osImageManifest = "version: 1\nrepository:\n  owner: fixture\n  name: fixture\nprofiles:\n  - os-image\n"

// pagesSiteManifest declares the pages-site profile, which no flavor implements, so the flavor
// stage records it as not applicable.
const pagesSiteManifest = "version: 1\nrepository:\n  owner: fixture\n  name: fixture\nprofiles:\n  - pages-site\n"

// kernelForgeRepo builds a kernel forge declaring os-image (#615) that meets the os-image
// flavor's requirements: a yamllint policy, lefthook.yml and the ruleset, beside the Go tooling
// goLibraryRepo lays down. extra adds forge files; its Kconfig fragments are what mark it.
func kernelForgeRepo(t *testing.T, extra map[string]string) string {
	t.Helper()
	files := map[string]string{
		".standards.yaml": osImageManifest,
		"versions.json":   "{\"stable\": \"6.12\"}\n",
		".yamllint.yml":   "extends: default\n",
	}
	for name, body := range extra {
		files[name] = body
	}
	return goLibraryRepo(t, files)
}

// TestRunFlavorStage_Positive_KernelForgePasses is #615's acceptance: a kernel forge with no
// Packer template or mkosi.conf gets an os-image verdict and passes once conforming, where it
// used to fail on a flavor resolution the operator could not act on.
func TestRunFlavorStage_Positive_KernelForgePasses(t *testing.T) {
	repo := kernelForgeRepo(t, map[string]string{"kconfig/base.config": "CONFIG_MODULES=y\n"})
	if msg, err := runFlavorStage(context.Background(), &stageConfig{repoDir: repo}); err != nil || msg != "" {
		t.Fatalf("a conforming kernel forge must pass the stage, got %q, %v", msg, err)
	}
}

// TestRunFlavorStage_Negative_UnmarkedOSImageStillFails keeps the stage fail-closed: a
// repository declaring os-image with no forge marker at all is not waved through as a pass or
// as not applicable. The reason names the flavor tried and no --flavor, which gate run lacks.
func TestRunFlavorStage_Negative_UnmarkedOSImageStillFails(t *testing.T) {
	_, err := runFlavorStage(context.Background(), &stageConfig{repoDir: kernelForgeRepo(t, nil)})
	if skip, skipped := errors.AsType[*stageSkip](err); skipped {
		t.Fatalf("an unmarked os-image repository was skipped as %s: %s", skip.status, skip.reason)
	}
	if !errors.Is(err, flavor.ErrNoFlavorMatched) {
		t.Fatalf("an unmarked os-image repository must fail with ErrNoFlavorMatched, got %v", err)
	}
	if reason := err.Error(); strings.Contains(reason, "--flavor") || !strings.Contains(reason, `profile "os-image" has flavors (os-image)`) {
		t.Errorf("the reason must name the flavor tried and no flag gate run lacks, got %q", reason)
	}
}

// TestRunFlavorStage_Boundary_KconfigOutputIsNoForge: the .config file Kconfig writes, at the
// root or under kconfig/, is not a fragment, so it still fails the stage like no marker at all.
func TestRunFlavorStage_Boundary_KconfigOutputIsNoForge(t *testing.T) {
	repo := kernelForgeRepo(t, map[string]string{".config": "CONFIG_X=y\n", "kconfig/.config": "CONFIG_X=y\n"})
	if _, err := runFlavorStage(context.Background(), &stageConfig{repoDir: repo}); !errors.Is(err, flavor.ErrNoFlavorMatched) {
		t.Fatalf("Kconfig output alone must not make a forge, got %v", err)
	}
}

// goLibraryRepo builds a repository the flavor catalog detects as go-library: a go.mod and
// an internal/ directory, all seven required templates, and both required settings. A case
// overwrites one file and reads the effect off the stage's verdict.
func goLibraryRepo(t *testing.T, settings map[string]string) string {
	t.Helper()
	files := map[string]string{
		"go.mod":                     "module example.com/fixture\n\ngo 1.25\n",
		"internal/keep.go":           "package internal\n",
		".standards.yaml":            "version: 1\n",
		".standards.lock":            "version: 1\n",
		".golangci.yml":              "version: \"2\"\n",
		".github/workflows/ci.yml":   "name: ci\non: push\njobs:\n  test:\n    runs-on: ubuntu-26.04\n    steps:\n      - run: make verify-all\n",
		".workingdir/STATE.md":       "# state\n",
		".workingdir/BUGS.md":        "# bugs\n",
		".workingdir/QUESTIONS.md":   "# questions\n",
		"lefthook.yml":               "pre-commit:\n  commands:\n    gofmt:\n      run: gofmt -l .\n",
		".github/rulesets/main.json": "{\"name\": \"main\", \"enforcement\": \"active\"}\n",
	}
	for name, body := range settings {
		files[name] = body
	}
	dir := t.TempDir()
	for name, body := range files {
		full := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			t.Fatalf("mkdir for %s: %v", name, err)
		}
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}

func TestRunFlavorStage_Positive_ConformingRepositoryPasses(t *testing.T) {
	msg, err := runFlavorStage(context.Background(), &stageConfig{repoDir: goLibraryRepo(t, nil)})
	if err != nil {
		t.Fatalf("a conforming repository must pass the stage: %v", err)
	}
	if msg != "" {
		t.Errorf("a passing stage carries no message, got %q", msg)
	}
}

// TestRunFlavorStage_Negative_FailureNamesTheInvalidSettings covers the report an operator
// gets when the push is already blocked. Settings are validated rather than counted, so a
// repository can fail this stage with every template present: two unparsable settings put
// this fixture at 7/9 = 77.8%. "0 missing templates" alone names no file to fix.
func TestRunFlavorStage_Negative_FailureNamesTheInvalidSettings(t *testing.T) {
	repo := goLibraryRepo(t, map[string]string{
		"lefthook.yml":               "pre-commit: [unterminated\n",
		".github/rulesets/main.json": "not json at all",
	})

	_, err := runFlavorStage(context.Background(), &stageConfig{repoDir: repo})
	if err == nil {
		t.Fatalf("two unparsable settings must fail the stage")
	}
	for _, want := range []string{"0 missing templates", "lefthook.yml", ".github/rulesets/main.json"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the failure must name %q, got %q", want, err)
		}
	}
}

// TestRunFlavorStage_Boundary_OneInvalidSettingStillClearsTheBar pins the other side: a
// single unparsable setting costs 1/9 and leaves the repository at 88.9%, above the bar, so
// the stage passes and reports nothing.
func TestRunFlavorStage_Boundary_OneInvalidSettingStillClearsTheBar(t *testing.T) {
	repo := goLibraryRepo(t, map[string]string{"lefthook.yml": "pre-commit: [unterminated\n"})

	msg, err := runFlavorStage(context.Background(), &stageConfig{repoDir: repo})
	if err != nil {
		t.Fatalf("88.9%% clears the 80%% bar: %v", err)
	}
	if msg != "" {
		t.Errorf("a passing stage carries no message, got %q", msg)
	}
}

// TestRunFlavorStage_Boundary_ProfileWithoutFlavorIsNotApplicable pins the verdict for a
// repository whose declared profile no flavor implements: not applicable, not passed.
// pages-site is the profile internal/flavor's own not-applicable case uses: no flavor
// implements it.
func TestRunFlavorStage_Boundary_ProfileWithoutFlavorIsNotApplicable(t *testing.T) {
	const profileWithoutFlavor = "pages-site"
	repo := goLibraryRepo(t, map[string]string{".standards.yaml": pagesSiteManifest})
	_, err := runFlavorStage(context.Background(), &stageConfig{repoDir: repo})
	if reason := wantSkip(t, err, StageNotApplicable); !strings.Contains(reason, profileWithoutFlavor) {
		t.Errorf("the verdict must name the profile, got %q", reason)
	}
}

// TestRunFlavorStage_Boundary_CancelledContextIsNotAVerdict keeps the stage's own I/O
// contract asserted: a cancelled context fails the stage rather than auditing anyway.
func TestRunFlavorStage_Boundary_CancelledContextIsNotAVerdict(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := runFlavorStage(ctx, &stageConfig{repoDir: goLibraryRepo(t, nil)}); err == nil {
		t.Fatalf("a cancelled context must not produce a pass")
	}
}
