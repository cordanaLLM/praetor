package main

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/devcontainer"
	"github.com/cordanaLLM/praetor/internal/testsupport"
	"github.com/cordanaLLM/praetor/internal/util"
)

// freshnessCheckout writes a Praetor source checkout whose manifest declares section, commits
// a ready bundle generated from it, and returns the manifest and bundle paths.
func freshnessCheckout(t *testing.T, section string) (string, string) {
	t.Helper()
	root := cliBootstrapSource(t)
	manifest := writeFixtureFile(t, root, ".standards.yaml", "version: 1\nrepository:\n  owner: adopted\n  name: app\nprofiles: []\nfacets: []\n"+section)
	writeFixtureFile(t, root, ".standards.lock", "version: 1\npinned_version: v1.0.0\ndigest: sha256:01ba4719c80b6fe911b091a7c05124b64eeece964e09c058ef8f9805daca546b\nprofiles: []\nfacets: []\n")
	output := filepath.Join(root, ".devcontainer", "devcontainer.json")
	if _, err := captureStdout(t, func() error {
		return runDevContainer([]string{"generate", "--config", manifest, "--output", output, "--source-root", root})
	}); err != nil {
		t.Fatal(err)
	}
	commitFreshnessFixture(t, root, "bundle", ".")
	return manifest, output
}

func commitFreshnessFixture(t *testing.T, root, message, path string) {
	t.Helper()
	ctx, err := util.WithCommandEnvironment(t.Context(), testsupport.HermeticGitEnv(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "--", path}, {"commit", "-q", "-m", message}} {
		if _, err := util.RunGit(ctx, root, args...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
}

func driftFreshnessFixture(t *testing.T, manifest, revision string) {
	t.Helper()
	root := filepath.Dir(manifest)
	writeFixtureFile(t, root, "cmd/standardsctl/main.go", "package main\n\n// "+revision+"\nfunc main() {}\n")
	commitFreshnessFixture(t, root, revision, "cmd/standardsctl/main.go")
}

func runFreshness(t *testing.T, manifest, output string) (string, error) {
	t.Helper()
	return captureStdout(t, func() error {
		return runDevContainer([]string{"freshness", "--config", manifest, "--output", output})
	})
}

// Positive: a fresh bundle prints [FRESH]; drift inside the manifest's bound prints the commit
// count under [DRIFT WITHIN BOUNDS] and passes.
func TestDevContainerFreshnessCLIPositive(t *testing.T) {
	manifest, output := freshnessCheckout(t, "devcontainer:\n  freshness:\n    max_commits: 1\n")
	message, err := runFreshness(t, manifest, output)
	if err != nil || !strings.Contains(message, "[FRESH]") {
		t.Fatalf("fresh bundle: %v %s", err, message)
	}
	driftFreshnessFixture(t, manifest, "first revision")
	message, err = runFreshness(t, manifest, output)
	if err != nil || !strings.Contains(message, "[DRIFT WITHIN BOUNDS]") || !strings.Contains(message, "1 commits and") {
		t.Fatalf("drift at the declared bound: %v %s", err, message)
	}
}

// Negative: drift past the manifest's bound fails with the count, an invalid bound fails before
// anything is measured, and a missing bundle is an error.
func TestDevContainerFreshnessCLINegative(t *testing.T) {
	manifest, output := freshnessCheckout(t, "devcontainer:\n  freshness:\n    max_commits: 1\n")
	driftFreshnessFixture(t, manifest, "first revision")
	driftFreshnessFixture(t, manifest, "second revision")
	if _, err := runFreshness(t, manifest, output); !errors.Is(err, devcontainer.ErrBundleStale) || !strings.Contains(err.Error(), "2 commits behind (bound 1)") {
		t.Fatalf("drift past the declared bound: %v", err)
	}
	invalid, invalidOutput := freshnessCheckout(t, "devcontainer:\n  freshness:\n    max_commits: 0\n")
	if _, err := runFreshness(t, invalid, invalidOutput); err == nil || !strings.Contains(err.Error(), "devcontainer.freshness.max_commits") {
		t.Fatalf("an invalid bound was accepted: %v", err)
	}
	if _, err := runFreshness(t, manifest, filepath.Join(filepath.Dir(output), "absent.json")); err == nil || errors.Is(err, devcontainer.ErrBundleStale) {
		t.Fatalf("a missing bundle passed or read as stale: %v", err)
	}
}

// Boundary: freshness accepts only the manifest and output options; every generation option and
// --verify is refused rather than ignored, and an unknown action names the supported ones.
func TestDevContainerFreshnessCLIOptions(t *testing.T) {
	opts, err := parseDevContainerOptions([]string{"freshness", "--config", "x.yaml", "--output", "y.json"})
	if err != nil || !opts.freshness || opts.verify || opts.configPath != "x.yaml" || opts.outputPath != "y.json" {
		t.Fatalf("freshness options = %+v, %v", opts, err)
	}
	for _, args := range [][]string{
		{"freshness", "--force"}, {"freshness", "--source-root", "/selected"}, {"freshness", "--verify"},
		{"freshness", "--builder-image", "mutable"}, {"freshness", "--base-image", "mutable"},
	} {
		if _, err := parseDevContainerOptions(args); err == nil {
			t.Fatalf("freshness accepted an option it ignores: %v", args)
		}
	}
	if _, err := parseDevContainerOptions([]string{"refresh"}); err == nil || !strings.Contains(err.Error(), "generate, verify, freshness") {
		t.Fatalf("unknown action: %v", err)
	}
}
