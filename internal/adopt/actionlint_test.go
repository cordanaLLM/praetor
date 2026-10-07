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
	"github.com/cordanaLLM/praetor/internal/flavor"
	"github.com/cordanaLLM/praetor/internal/managedasset"
	"github.com/cordanaLLM/praetor/templates"
	"gopkg.in/yaml.v3"
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

// Boundary: with docs:seo-portal disabled and no flavor detected no managed workflow needs a
// label, so nothing is created; a dry run reports the creation it would make without writing it.
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

// Negative: an adoption context that ends while the node contract is checked fails the merge
// with the context error, never with a refusal the step would report as a finding, for a file
// it would patch and for one it would refuse alike.
func TestMergeActionlintLabelsNegativeCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for name, content := range map[string]string{
		"patchable": "config-variables: null\n",
		"anchor":    "self-hosted-runner: &runner\n  labels: [gpu]\n",
	} {
		t.Run(name, func(t *testing.T) {
			merged, added, err := mergeActionlintLabels(ctx, []byte(content), []string{"ubuntu-26.04"})
			var refusal actionlintRefusal
			if !errors.Is(err, context.Canceled) || errors.As(err, &refusal) || merged != nil || added != nil {
				t.Fatalf("merged=%q added=%v err=%v, want context.Canceled", merged, added, err)
			}
		})
	}
}

// Positive (#622): a Go library adopted without docs:seo-portal writes no documentation gate,
// yet the CI workflow its flavor writes runs on a label actionlint does not know, so adoption
// declares that label; the rerun leaves the file as it is.
func TestActionlintLabelsPositiveDeclaresFlavorWorkflowRunner(t *testing.T) {
	repo := newTestRepo(t, "actionlint-flavor")
	for rel, body := range goLibrary {
		mustWrite(t, filepath.Join(repo, filepath.FromSlash(rel)), body)
	}
	opts := AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repo, Profile: "framework", Facets: []string{"custom:facet"}}
	report, err := Adopt(t.Context(), opts)
	if err != nil {
		t.Fatalf("adopt: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(repo, filepath.FromSlash(DocumentationWorkflowFile))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("adoption without docs:seo-portal wrote the documentation gate: %v", err)
	}
	if !strings.Contains(mustRead(t, filepath.Join(repo, ".github", "workflows", "ci.yml")), "runs-on: ubuntu-26.04") {
		t.Fatal("the flavor wrote no CI workflow on ubuntu-26.04")
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

// Negative (#622): without docs:seo-portal, a workflow adoption does not write needs no label
// from it: a declined flavor step writes no CI workflow, and a ci.yml of the repository's own on
// the same runner is the repository's to declare. Neither creates a configuration.
func TestActionlintLabelsNegativeSkipsWorkflowsAdoptionDoesNotWrite(t *testing.T) {
	declined := ciSession(t, "working-dir-and-flavor")
	owned := ciSession(t)
	mustWrite(t, filepath.Join(owned.repoPath, ".github", "workflows", "ci.yml"),
		"name: Own\non: push\njobs:\n  own:\n    runs-on: ubuntu-26.04\n    steps:\n      - run: make ci\n")
	for name, s := range map[string]*adoptSession{"declined flavor step": declined, "own ci.yml": owned} {
		t.Run(name, func(t *testing.T) {
			s.facets = []string{"custom:facet"}
			if err := reconcileActionlintLabels(t.Context(), s); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Lstat(filepath.Join(s.repoPath, ".github", "actionlint.yaml")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("a configuration was created: %v", err)
			}
			if detail := findActionDetail(s.report.ActionDetails, actionlintConfigFile); !strings.Contains(detail, "No Praetor-managed workflow") {
				t.Fatalf("not reported as not applicable: %q", detail)
			}
		})
	}
}

// Boundary (#622): the documentation gate and the Go CI workflow both run on ubuntu-26.04, and
// the label is declared once; a workflow on a runner actionlint knows, or on an expression,
// needs none; no workflow needs none; and a workflow that is not YAML fails the step, naming it.
func TestActionlintLabelsOfBoundary(t *testing.T) {
	docs := flavor.PlannedTemplate{Path: DocumentationWorkflowFile, Content: DocumentationWorkflow()}
	goCI := flavor.PlannedTemplate{Path: ".github/workflows/ci.yml", Content: renderedTemplate(t, "go/ci-go.yml.tmpl")}
	known := flavor.PlannedTemplate{Path: "known.yml", Content: "jobs:\n  a:\n    runs-on: ubuntu-24.04\n  b:\n    runs-on: ${{ matrix.os }}\n"}
	for name, tc := range map[string]struct {
		files []flavor.PlannedTemplate
		want  []string
	}{
		"docs and flavor": {[]flavor.PlannedTemplate{docs, goCI}, []string{"ubuntu-26.04"}},
		"flavor only":     {[]flavor.PlannedTemplate{goCI}, []string{"ubuntu-26.04"}},
		"known runners":   {[]flavor.PlannedTemplate{known}, nil},
		"no workflow":     {nil, nil},
	} {
		got, err := actionlintLabelsOf(tc.files)
		if err != nil || !slices.Equal(got, tc.want) {
			t.Errorf("%s: labels = %q (%v), want %q", name, got, err, tc.want)
		}
	}
	broken := flavor.PlannedTemplate{Path: "broken.yml", Content: "jobs: [\n"}
	if _, err := actionlintLabelsOf([]flavor.PlannedTemplate{goCI, broken}); err == nil || !strings.Contains(err.Error(), "broken.yml") {
		t.Fatalf("a workflow that is not YAML was not reported by name: %v", err)
	}
}

// Positive (#357): a Go repository adopted with api:public-contract alone and its flavor step
// declined gets no workflow but the API compatibility gate, which runs on a label actionlint does
// not know, so adoption declares that label. Negative: without the facet, or with it but a go.mod
// git does not track, the gate is not emitted and no label is declared for it. Boundary: in every
// case the label is declared exactly when the gate step emitted the workflow, since both read
// the same enabled families (enabledManagedFamiliesForSession, adoptedWorkflowFiles).
func TestActionlintLabelsFollowTheAPICompatibilityGate(t *testing.T) {
	for name, tc := range map[string]struct {
		facets  []string
		tracked bool
		want    bool
	}{
		"facet and tracked go.mod": {[]string{"api:public-contract"}, true, true},
		"no facet":                 {[]string{"custom:facet"}, true, false},
		"untracked go.mod":         {[]string{"api:public-contract"}, false, false},
	} {
		t.Run(name, func(t *testing.T) {
			repo := newTestRepo(t, "actionlint-api")
			if tc.tracked {
				trackGoModule(t, repo, "go.mod")
			} else {
				mustWrite(t, filepath.Join(repo, "go.mod"), "module example.com/widget\n\ngo 1.27\n")
			}
			s := actionlintSession(repo, tc.facets)
			s.declined = []string{"working-dir-and-flavor"}
			if err := reconcileAPICompatibilityGate(t.Context(), s); err != nil {
				t.Fatalf("reconcile the API compatibility gate: %v", err)
			}
			_, err := os.Lstat(filepath.Join(repo, filepath.FromSlash(APICompatibilityWorkflowFile)))
			emitted := err == nil
			if err := reconcileActionlintLabels(t.Context(), s); err != nil {
				t.Fatalf("reconcile actionlint labels: %v", err)
			}
			config, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(actionlintConfigFile)))
			declared := err == nil && string(config) == createdActionlintConfig
			if emitted != tc.want || declared != emitted {
				t.Fatalf("gate emitted=%v, label declared=%v, want both %v (configuration: %q, %v)", emitted, declared, tc.want, config, err)
			}
		})
	}
}

// Boundary (#622): actionlintUnknownLabels holds at least one label, each once and at most
// maxActionlintLabels of them; every one is a runner some emitted workflow runs on, so the list
// claims nothing about a runner no workflow uses; and each reads back from the configuration
// adoption renders as the same string, so the label declared is the label the workflows name.
func TestActionlintUnknownLabelsBoundary(t *testing.T) {
	labels := actionlintUnknownLabels[:]
	if len(labels) == 0 || len(labels) > maxActionlintLabels {
		t.Fatalf("%d labels, want 1..%d; with none left, drop the step with the list", len(labels), maxActionlintLabels)
	}
	used, err := actionlintLabelsOf(emittedWorkflows(t))
	if err != nil || !slices.Equal(used, labels) {
		t.Fatalf("labels emitted workflows run on = %q (%v), want every listed label %q", used, err, labels)
	}
	for index, label := range labels {
		var decoded struct {
			Runner struct {
				Labels []any `yaml:"labels"`
			} `yaml:"self-hosted-runner"`
		}
		if err := yaml.Unmarshal(renderActionlintConfig([]string{label}), &decoded); err != nil {
			t.Fatalf("%s: rendered configuration does not decode: %v", label, err)
		}
		if slices.Index(labels, label) != index || !slices.Equal(decoded.Runner.Labels, []any{label}) {
			t.Errorf("label %q is repeated or reads back as %#v", label, decoded.Runner.Labels)
		}
	}
}

// Positive (#593, #622 acceptance): wherever actionlint is on PATH, it accepts every workflow
// adoption can write, every managed family's hosted workflow and each flavor's CI workflows in
// every rendering,
// beside the configuration adoption creates for that workflow alone, or none when it needs no
// label.
func TestActionlintAcceptsEveryEmittedWorkflow(t *testing.T) {
	bin := requireActionlint(t)
	repo := actionlintFixtureRepo(t)
	configPath := filepath.Join(repo, filepath.FromSlash(actionlintConfigFile))
	for _, workflow := range emittedWorkflows(t) {
		labels, err := actionlintLabelsOf([]flavor.PlannedTemplate{workflow})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(configPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		if len(labels) > 0 {
			mustWrite(t, configPath, string(renderActionlintConfig(labels)))
		}
		if out, err := runActionlint(t, bin, repo, workflow.Content); err != nil {
			t.Errorf("actionlint rejects %s beside the configuration adoption creates for it: %v\n%s", workflow.Path, err, out)
		}
	}
}

// Boundary (#593, #622): wherever actionlint is on PATH, it still rejects the emitted workflows
// without the declaration, label by label. Once it accepts a label, actionlint has shipped it:
// remove the label from actionlintUnknownLabels, and adoption stops adding it.
func TestActionlintStillRejectsTheDeclaredLabels(t *testing.T) {
	bin := requireActionlint(t)
	repo := actionlintFixtureRepo(t)
	var rejected strings.Builder
	for _, workflow := range emittedWorkflows(t) {
		out, err := runActionlint(t, bin, repo, workflow.Content)
		if err != nil {
			rejected.WriteString(out)
		}
	}
	for _, label := range actionlintUnknownLabels {
		if !strings.Contains(rejected.String(), `label "`+label+`" is unknown`) {
			t.Errorf("actionlint knows %s now; remove it from actionlintUnknownLabels:\n%s", label, rejected.String())
		}
	}
}

// requireActionlint returns the actionlint on PATH, skipping when there is none: no workflow
// installs it, so the check runs where a developer's hooks already use it. Those hooks hold
// it to the floor in .config/lefthook/tool-floors.txt.
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
	labels, err := actionlintManagedLabels(t.Context(), actionlintSession(t.TempDir(), config.DefaultFacets()))
	if err != nil || len(labels) == 0 {
		t.Fatalf("the default facets declare no actionlint label: %v", err)
	}
	return labels
}

// renderedWorkflowsRoot is templates/branching_test.go's renderedRoot seen from this package: the
// committed renderings of each template whose actions branch on repository facts.
var renderedWorkflowsRoot = filepath.Join("..", "..", templates.Directory, "testdata", "rendered")

// emittedWorkflows returns every workflow body adoption can write, each named for messages: the
// hosted workflow of every managed asset family, the documentation gate and the Go API
// compatibility gate among them (familyWorkflows), and each CI workflow template of every flavor,
// once per distinct rendering. A template whose actions branch on repository facts is read from
// its committed renderings, which TestBranchingYAMLTemplateRenderingsAreCommitted keeps equal to
// every body it renders; any other is rendered as flavor apply renders it.
func emittedWorkflows(t *testing.T) []flavor.PlannedTemplate {
	t.Helper()
	workflows, err := familyWorkflows(t.Context(), t.TempDir(), managedasset.Families())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(workflows, func(workflow flavor.PlannedTemplate) bool { return workflow.Path == APICompatibilityWorkflowFile }) {
		t.Fatalf("the managed family workflows %v lack %s", workflows, APICompatibilityWorkflowFile)
	}
	var sources []string
	for _, flv := range flavor.List() {
		for _, item := range flv.RequiredTemplates() {
			if !strings.HasPrefix(item.Path, ".github/workflows/") || item.Source == "" || slices.Contains(sources, item.Source) {
				continue
			}
			sources = append(sources, item.Source)
			workflows = append(workflows, templateRenderings(t, item.Source)...)
		}
	}
	if len(sources) == 0 {
		t.Fatal("no flavor declares a CI workflow template")
	}
	return workflows
}

// templateRenderings returns every committed rendering of source, or its one rendering when it
// has no committed renderings.
func templateRenderings(t *testing.T, source string) []flavor.PlannedTemplate {
	t.Helper()
	dir := filepath.Join(renderedWorkflowsRoot, filepath.FromSlash(strings.TrimSuffix(source, ".tmpl")))
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return []flavor.PlannedTemplate{{Path: source, Content: renderedTemplate(t, source)}}
	}
	if err != nil || len(entries) == 0 {
		t.Fatalf("read the renderings of %s: %d entries (%v)", source, len(entries), err)
	}
	renderings := make([]flavor.PlannedTemplate, 0, len(entries))
	for _, entry := range entries {
		renderings = append(renderings, flavor.PlannedTemplate{Path: source + " as " + entry.Name(), Content: mustRead(t, filepath.Join(dir, entry.Name()))})
	}
	return renderings
}

// actionlintFixtureRepo returns an empty checkout, the project root actionlint reads its
// configuration from.
func actionlintFixtureRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	initTestGit(t, repo)
	return repo
}

// runActionlint writes workflow into repo and runs bin over it, without the shellcheck and
// pyflakes integrations, whose presence varies by host.
func runActionlint(t *testing.T, bin, repo, workflow string) (string, error) {
	t.Helper()
	rel := filepath.Join(".github", "workflows", "emitted.yml")
	mustWrite(t, filepath.Join(repo, rel), workflow)
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "-no-color", "-oneline", "-shellcheck=", "-pyflakes=", rel)
	cmd.Dir = repo
	out, err := cmd.CombinedOutput()
	return string(out), err
}
