// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
)

// createdActionlintConfig is the configuration adoption creates, spelled out rather than read
// from the code under test.
const createdActionlintConfig = `---
# praetorctl adopt wrote this file for actionlint, which rejects a runs-on label
# missing from its built-in table of GitHub-hosted runners unless it is declared
# under self-hosted-runner.labels. Each label below is one a Praetor-managed
# workflow runs on that actionlint v1.7.12 does not know. Adoption only adds a
# missing label and never removes one, so keep your own settings here, and
# delete a label once your actionlint accepts .github/workflows without it.
self-hosted-runner:
  labels:
    - ubuntu-26.04
`

// actionlintInitConfig is what actionlint v1.7.12 -init-config writes, verbatim.
const actionlintInitConfig = `self-hosted-runner:
  # Labels of self-hosted runner in array of strings.
  labels: []

# Configuration variables in array of strings defined in your repository or
# organization. ` + "`null`" + ` means disabling configuration variables check.
# Empty array means no configuration variable is allowed.
config-variables: null

# Configuration for file paths. The keys are glob patterns to match to file
# paths relative to the repository root. The values are the configurations for
# the file paths. Note that the path separator is always '/'.
# The following configurations are available.
#
# "ignore" is an array of regular expression patterns. Matched error messages
# are ignored. This is similar to the "-ignore" command line option.
paths:
#  .github/workflows/**/*.yml:
#    ignore: []
`

// actionlintSession is an adoption session over repo with the given facets.
func actionlintSession(repo string, facets []string) *adoptSession {
	return &adoptSession{repoPath: repo, facets: facets, opts: AdoptOptions{Path: repo},
		report: &AdoptReport{DebtBreakdown: map[string]int{}}}
}

// reconcileActionlintIn runs the actionlint step over repo with the default facets.
func reconcileActionlintIn(t *testing.T, repo string) *AdoptReport {
	t.Helper()
	s := actionlintSession(repo, config.DefaultFacets())
	if err := reconcileActionlintLabels(t.Context(), s); err != nil {
		t.Fatalf("reconcile actionlint labels: %v", err)
	}
	return s.report
}

// Positive (#593): adopting docs:seo-portal into a repository without an actionlint
// configuration creates .github/actionlint.yaml declaring the documentation gate's runner, and
// the rerun leaves it as it is.
func TestActionlintLabelsPositiveCreatesConfiguration(t *testing.T) {
	repo := newTestRepo(t, "actionlint-create")
	opts := AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repo}
	report, err := Adopt(t.Context(), opts)
	if err != nil {
		t.Fatalf("adopt: %v", err)
	}
	path := filepath.Join(repo, ".github", "actionlint.yaml")
	if got := mustRead(t, path); got != createdActionlintConfig {
		t.Fatalf("created configuration:\n%s\nwant:\n%s", got, createdActionlintConfig)
	}
	if detail := findActionDetail(report.ActionDetails, actionlintConfigFile); !strings.Contains(detail, "it does not know: ubuntu-26.04") {
		t.Fatalf("creation not reported: %q", detail)
	}
	report, err = Adopt(t.Context(), opts)
	if err != nil || mustRead(t, path) != createdActionlintConfig ||
		!strings.Contains(findActionDetail(report.ActionDetails, actionlintConfigFile), "already accepts") {
		t.Fatalf("rerun is not a no-op: %v\n%s", err, mustRead(t, path))
	}
}

// Positive: the configuration actionlint -init-config writes gets the label inside its empty
// flow list and keeps every other byte, in its CRLF checkout too.
func TestActionlintLabelsPositiveExtendsInitConfig(t *testing.T) {
	want := strings.Replace(actionlintInitConfig, "labels: []", "labels: [ubuntu-26.04]", 1)
	for name, eol := range map[string]string{"LF": "\n", "CRLF": "\r\n"} {
		t.Run(name, func(t *testing.T) {
			repo := t.TempDir()
			path := filepath.Join(repo, ".github", "actionlint.yaml")
			mustWrite(t, path, strings.ReplaceAll(actionlintInitConfig, "\n", eol))
			report := reconcileActionlintIn(t, repo)
			if got := mustRead(t, path); got != strings.ReplaceAll(want, "\n", eol) {
				t.Fatalf("merged configuration:\n%q", got)
			}
			if detail := findActionDetail(report.ActionDetails, actionlintConfigFile); !strings.HasPrefix(detail, "Declared ubuntu-26.04 under self-hosted-runner.labels") {
				t.Fatalf("merge not reported: %q", detail)
			}
		})
	}
}

// Negative: an adopter's configuration keeps every label and setting it declares: its own
// labels stay first, a pattern of its own that covers the label leaves the file untouched, and
// only the file actionlint reads is edited.
func TestActionlintLabelsNegativeKeepsAdopterSettings(t *testing.T) {
	const own = "self-hosted-runner:\n  labels:\n    - gpu  # our runners\n    - arm64\n\n  # trailing note\nconfig-variables:\n  - DEPLOY_ENV\npaths:\n  .github/workflows/legacy.yml:\n    ignore:\n      - 'shellcheck reported issue'\n"
	repo := t.TempDir()
	path := filepath.Join(repo, ".github", "actionlint.yaml")
	mustWrite(t, path, own)
	reconcileActionlintIn(t, repo)
	want := strings.Replace(own, "    - arm64\n", "    - arm64\n    - ubuntu-26.04\n", 1)
	if got := mustRead(t, path); got != want {
		t.Fatalf("merged configuration:\n%s\nwant:\n%s", got, want)
	}

	covered := "self-hosted-runner:\n  labels: ['ubuntu-*']\n"
	mustWrite(t, path, covered)
	report := reconcileActionlintIn(t, repo)
	if mustRead(t, path) != covered || !strings.Contains(findActionDetail(report.ActionDetails, actionlintConfigFile), "already accepts") {
		t.Fatalf("a covering pattern did not leave the file alone:\n%s", mustRead(t, path))
	}

	ymlOnly := t.TempDir()
	yml := filepath.Join(ymlOnly, ".github", "actionlint.yml")
	mustWrite(t, yml, "config-variables: null\n")
	reconcileActionlintIn(t, ymlOnly)
	if got := mustRead(t, yml); got != "config-variables: null\nself-hosted-runner:\n  labels:\n    - ubuntu-26.04\n" {
		t.Fatalf("the .yml configuration was not extended:\n%s", got)
	}
	if _, err := os.Lstat(filepath.Join(ymlOnly, ".github", "actionlint.yaml")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a .yaml configuration was created beside the .yml one: %v", err)
	}

	both := t.TempDir()
	mustWrite(t, filepath.Join(both, ".github", "actionlint.yaml"), "{}\n")
	mustWrite(t, filepath.Join(both, ".github", "actionlint.yml"), "config-variables: null\n")
	reconcileActionlintIn(t, both)
	if mustRead(t, filepath.Join(both, ".github", "actionlint.yml")) != "config-variables: null\n" {
		t.Fatal("the .yml configuration actionlint does not read was edited")
	}
}

// Negative: a configuration adoption will not patch is reported with the labels to declare by
// hand and left byte for byte.
func TestActionlintLabelsNegativeReportsUnpatchableConfiguration(t *testing.T) {
	for name, content := range map[string]string{
		"flow root":           "{self-hosted-runner: {labels: [gpu]}}\n",
		"flow runner":         "self-hosted-runner: {labels: [gpu]}\n",
		"runner list":         "self-hosted-runner: [gpu]\n",
		"labels scalar":       "self-hosted-runner:\n  labels: gpu\n",
		"multi-line flow":     "self-hosted-runner:\n  labels: [\n    gpu,\n  ]\n",
		"null on its own":     "self-hosted-runner:\n  labels:\n    ~\n",
		"anchor":              "self-hosted-runner: &runner\n  labels: [gpu]\n",
		"duplicate key":       "self-hosted-runner:\n  labels: [gpu]\nself-hosted-runner:\n  labels: []\n",
		"two documents":       "---\nconfig-variables: null\n---\npaths: {}\n",
		"document end":        "config-variables: null\n...\n",
		"mixed line endings":  "config-variables: null\r\npaths: {}\n",
		"top-level list":      "- gpu\n",
		"indented root":       "  config-variables: null\n",
		"not YAML":            "self-hosted-runner: [\n",
		"labels of a mapping": "self-hosted-runner:\n  labels:\n    gpu: true\n",
	} {
		t.Run(name, func(t *testing.T) {
			repo := t.TempDir()
			path := filepath.Join(repo, ".github", "actionlint.yaml")
			mustWrite(t, path, content)
			report := reconcileActionlintIn(t, repo)
			if got := mustRead(t, path); got != content {
				t.Fatalf("configuration changed:\n%q", got)
			}
			warnings := strings.Join(report.Warnings, "\n")
			if !strings.Contains(warnings, actionlintConfigFile+": actionlint configuration left untouched: ") ||
				!strings.Contains(warnings, "declare ubuntu-26.04 under its self-hosted-runner.labels") {
				t.Fatalf("not reported with the label to declare by hand:\n%s", warnings)
			}
		})
	}
}

// Negative: a configuration adoption may not read, a directory at the path actionlint reads
// first, is reported and stops the search, so the .yml file behind it is not edited; a
// cancelled context is returned, never reported as a finding.
func TestActionlintLabelsNegativeReportsUninspectableConfiguration(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".github", "actionlint.yaml"), 0o755); err != nil {
		t.Fatal(err)
	}
	yml := filepath.Join(repo, ".github", "actionlint.yml")
	mustWrite(t, yml, "config-variables: null\n")
	report := reconcileActionlintIn(t, repo)
	if !strings.Contains(strings.Join(report.Warnings, "\n"), actionlintConfigFile+": actionlint configuration left untouched: it cannot be read safely") {
		t.Fatalf("directory not reported: %v", report.Warnings)
	}
	if mustRead(t, yml) != "config-variables: null\n" {
		t.Fatal("the .yml file behind an uninspectable .yaml path was edited")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	s := actionlintSession(repo, config.DefaultFacets())
	if rel, _, _, err := findActionlintConfig(ctx, s, []string{"ubuntu-26.04"}); rel != "" || !errors.Is(err, context.Canceled) {
		t.Fatalf("rel=%q err=%v, want context.Canceled", rel, err)
	}
	if len(s.report.Warnings) != 0 {
		t.Fatalf("cancellation reported as a finding: %v", s.report.Warnings)
	}
}

// Boundary: with docs:seo-portal disabled no managed workflow needs a label, so nothing is
// created and an adopter's configuration stays as written; a dry run reports the creation it
// would make without writing it.
func TestActionlintLabelsBoundaryFacetAndDryRun(t *testing.T) {
	repo := t.TempDir()
	s := actionlintSession(repo, []string{"custom:facet"})
	if err := reconcileActionlintLabels(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(repo, ".github")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a disabled facet created files: %v", err)
	}
	if detail := findActionDetail(s.report.ActionDetails, actionlintConfigFile); !strings.Contains(detail, "No Praetor-managed workflow") {
		t.Fatalf("not reported as not applicable: %q", detail)
	}
	dry := actionlintSession(repo, config.DefaultFacets())
	dry.opts.DryRun = true
	if err := reconcileActionlintLabels(t.Context(), dry); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(repo, ".github")); !errors.Is(err, os.ErrNotExist) || !strings.Contains(strings.Join(dry.report.CreatedFiles, ","), actionlintConfigFile) {
		t.Fatalf("dry run wrote or did not report the creation: %v %v", err, dry.report.CreatedFiles)
	}
}

// Boundary: every layout the patch extends, in isolation, and the empty label set, which
// changes nothing.
func TestMergeActionlintLabelsBoundaryLayouts(t *testing.T) {
	block := "self-hosted-runner:\n  labels:\n    - ubuntu-26.04\n"
	for name, tc := range map[string]struct{ in, want string }{
		"empty file":          {"", block},
		"comments only":       {"# ours\n", "# ours\n" + block},
		"document start only": {"---\n", "---\n" + block},
		"no final newline":    {"config-variables: null", "config-variables: null\n" + block},
		"four-space indent":   {"paths:\n    x.yml:\n        ignore: []\n", "paths:\n    x.yml:\n        ignore: []\nself-hosted-runner:\n    labels:\n        - ubuntu-26.04\n"},
		"null runner":         {"self-hosted-runner:\nx: 1\n", "self-hosted-runner:\n  labels:\n    - ubuntu-26.04\nx: 1\n"},
		"tilde runner":        {"self-hosted-runner: ~ # none yet\n", "self-hosted-runner: # none yet\n  labels:\n    - ubuntu-26.04\n"},
		"runner without list": {"self-hosted-runner:\n  other: 1\n\n# next\nx: 1\n", "self-hosted-runner:\n  other: 1\n  labels:\n    - ubuntu-26.04\n\n# next\nx: 1\n"},
		"null labels":         {"self-hosted-runner:\n  labels:\nx: 1\n", "self-hosted-runner:\n  labels:\n    - ubuntu-26.04\nx: 1\n"},
		"indentless list":     {"self-hosted-runner:\n  labels:\n  - gpu\n", "self-hosted-runner:\n  labels:\n  - gpu\n  - ubuntu-26.04\n"},
		"flow list":           {"self-hosted-runner:\n  labels: [gpu, 'arm64']  # ours ]\n", "self-hosted-runner:\n  labels: [gpu, 'arm64', ubuntu-26.04]  # ours ]\n"},
	} {
		t.Run(name, func(t *testing.T) {
			merged, added, err := mergeActionlintLabels(t.Context(), []byte(tc.in), []string{"ubuntu-26.04"})
			if err != nil || string(merged) != tc.want || !slices.Equal(added, []string{"ubuntu-26.04"}) {
				t.Fatalf("err=%v added=%v\n%q\nwant\n%q", err, added, merged, tc.want)
			}
		})
	}
	merged, added, err := mergeActionlintLabels(t.Context(), []byte(actionlintInitConfig), nil)
	if err != nil || len(added) != 0 || merged != nil {
		t.Fatalf("an empty label set changed the file: %q %v %v", merged, added, err)
	}
}

// Positive (#593 acceptance): wherever actionlint is on PATH, it accepts the adopted
// documentation workflow beside the configuration adoption creates.
func TestActionlintAcceptsTheAdoptedWorkflow(t *testing.T) {
	bin := requireActionlint(t)
	repo := actionlintFixtureRepo(t)
	mustWrite(t, filepath.Join(repo, ".github", "actionlint.yaml"), string(renderActionlintConfig(actionlintManagedFixtureLabels(t))))
	if out, err := runActionlint(t, bin, repo); err != nil {
		t.Fatalf("actionlint rejects the adopted workflow: %v\n%s", err, out)
	}
}

// Boundary (#593): wherever actionlint is on PATH, it still rejects the documentation workflow
// without the declaration, label by label. Once it accepts a label, actionlint has shipped it:
// remove the label from tools/markdownlint's actionlintLabels, and adoption stops adding it.
func TestActionlintStillRejectsTheDeclaredLabels(t *testing.T) {
	bin := requireActionlint(t)
	out, err := runActionlint(t, bin, actionlintFixtureRepo(t))
	if err == nil {
		t.Fatal("actionlint accepts the documentation workflow without a declaration; remove the labels it knows from actionlintLabels")
	}
	for _, label := range actionlintManagedFixtureLabels(t) {
		if !strings.Contains(out, `label "`+label+`" is unknown`) {
			t.Errorf("actionlint knows %s now; remove it from tools/markdownlint's actionlintLabels:\n%s", label, out)
		}
	}
}

// requireActionlint returns the actionlint on PATH, skipping when there is none: praetor does
// not install or pin it (#343), so the check runs where a developer's hooks already use it.
func requireActionlint(t *testing.T) string {
	t.Helper()
	bin, err := exec.LookPath("actionlint")
	if err != nil {
		t.Skip("actionlint is not on PATH; this check runs where it is installed")
	}
	return bin
}

// actionlintManagedFixtureLabels returns the labels the default facets declare to actionlint.
func actionlintManagedFixtureLabels(t *testing.T) []string {
	t.Helper()
	labels, err := actionlintManagedLabels(actionlintSession(t.TempDir(), config.DefaultFacets()))
	if err != nil || len(labels) == 0 {
		t.Fatalf("the default facets declare no actionlint label: %v", err)
	}
	return labels
}

// actionlintFixtureRepo returns a checkout holding only the documentation workflow.
func actionlintFixtureRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	initTestGit(t, repo)
	mustWrite(t, filepath.Join(repo, filepath.FromSlash(DocumentationWorkflowFile)), DocumentationWorkflow())
	return repo
}

// runActionlint runs bin over the documentation workflow of repo, without the shellcheck and
// pyflakes integrations, whose presence varies by host.
func runActionlint(t *testing.T, bin, repo string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "-no-color", "-oneline", "-shellcheck=", "-pyflakes=", filepath.FromSlash(DocumentationWorkflowFile))
	cmd.Dir = repo
	out, err := cmd.CombinedOutput()
	return string(out), err
}
