// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/adopt"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/testsupport"
	apiassets "github.com/cordanaLLM/praetor/tools/apicompat"
)

// apiGateManifest declares the public API contract facet.
func apiGateManifest() *config.Manifest {
	return &config.Manifest{Facets: []string{"api:public-contract"}}
}

// apiGateFixture is a committed repository holding the gate's locked texts, a ruleset that
// requires its context, and a go.mod at gomod unless gomod is empty.
func apiGateFixture(t *testing.T, gomod string) string {
	t.Helper()
	root := t.TempDir()
	if gomod != "" {
		writeFixtureFile(t, root, gomod, "module example.com/widget\n\ngo 1.27\n")
	}
	for _, family := range adopt.APICompatibilityFamilies() {
		for _, name := range family.Names() {
			data, err := family.Read(name)
			if err != nil {
				t.Fatal(err)
			}
			writeFixtureFile(t, root, family.AssetPath(name), string(data))
		}
		writeFixtureFile(t, root, family.WorkflowFile, family.Workflow)
	}
	ruleset, err := forge.RenderRepositoryRuleset("main", config.DefaultPolicy().BranchProtection,
		[]string{adopt.APICompatibilityStatusContext})
	if err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, root, ".github/rulesets/main.json", string(ruleset))
	initGitFixture(t, root)
	return root
}

// Positive: the locked, committed gate passes, in LF and in one consistent CRLF style.
func TestAuditAPICompatibilityGate_Positive_LockedGatePasses(t *testing.T) {
	root := apiGateFixture(t, "go.mod")
	if err := auditAPICompatibilityGate(t.Context(), apiGateManifest(), root); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{apiassets.WorkflowFile, apiassets.Directory + "/" + apiassets.GateFile} {
		writeFixtureFile(t, root, rel, strings.ReplaceAll(readFixtureFile(t, root, rel), "\n", "\r\n"))
	}
	if err := auditAPICompatibilityGate(t.Context(), apiGateManifest(), root); err != nil {
		t.Fatalf("consistent CRLF gate failed audit: %v", err)
	}
}

// Negative: an edited gate program, an edited or missing workflow, and gate files git ignores
// and does not track each fail the gate.
func TestAuditAPICompatibilityGate_Negative_DriftFails(t *testing.T) {
	gate := apiassets.Directory + "/" + apiassets.GateFile
	for name, tc := range map[string]struct {
		mutate func(t *testing.T, root string)
		want   string
	}{
		"edited gate program": {func(t *testing.T, root string) {
			writeFixtureFile(t, root, gate, strings.Replace(apiassets.Workflow, "fetch-depth", "depth", 1))
		}, "differs from the locked Praetor asset"},
		"conditional workflow job": {func(t *testing.T, root string) {
			writeFixtureFile(t, root, apiassets.WorkflowFile, strings.Replace(apiassets.Workflow,
				"    name: "+apiassets.StatusContext+"\n", "    name: "+apiassets.StatusContext+"\n    if: false\n", 1))
		}, "differs from the locked Praetor asset"},
		"missing workflow": {func(t *testing.T, root string) {
			if err := os.Remove(filepath.Join(root, filepath.FromSlash(apiassets.WorkflowFile))); err != nil {
				t.Fatal(err)
			}
		}, "is missing or unreadable"},
		"ignored untracked gate": {func(t *testing.T, root string) {
			if out, err := runFixtureGit(t, root, testsupport.HermeticGitEnv(t), "rm", "-q", "-r", "--cached", apiassets.Directory); err != nil {
				t.Skipf("git rm --cached failed in sandbox: %v (%s)", err, out)
			}
			writeFixtureFile(t, root, ".gitignore", "tools/\n")
		}, "API compatibility gate files are ignored by git and not tracked"},
	} {
		t.Run(name, func(t *testing.T) {
			root := apiGateFixture(t, "go.mod")
			tc.mutate(t, root)
			err := auditAPICompatibilityGate(t.Context(), apiGateManifest(), root)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("audit = %v, want a failure naming %q", err, tc.want)
			}
		})
	}
}

// Negative and boundary: the context check reads the workflow itself, so a family whose
// workflow gates its job on a condition, or leaves it advisory, is refused, and one whose job
// reports on every pull request passes.
func TestAuditFamilyContextsReported(t *testing.T) {
	family := adopt.APICompatibilityFamilies()[0]
	job := "    name: " + family.StatusContext + "\n"
	for name, tc := range map[string]struct {
		workflow string
		pass     bool
	}{
		"every pull request": {family.Workflow, true},
		"conditional job":    {strings.Replace(family.Workflow, job, job+"    if: github.event_name == 'push'\n", 1), false},
		"advisory job":       {strings.Replace(family.Workflow, job, job+"    continue-on-error: true\n", 1), false},
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			writeFixtureFile(t, root, family.WorkflowFile, tc.workflow)
			err := auditFamilyContextsReported(t.Context(), root, adopt.APICompatibilityFamilies())
			if tc.pass != (err == nil) {
				t.Fatalf("audit = %v, want pass %v", err, tc.pass)
			}
		})
	}
}

// Disabled facet. Negative: Praetor's gate files left behind, or a ruleset still requiring the
// context, fail. Boundary: an operator's own file at a managed path passes, and a declined
// branch ruleset is not read.
func TestAuditAPICompatibilityGate_Disabled(t *testing.T) {
	manifest := &config.Manifest{Facets: []string{"docs:seo-portal"}}
	root := apiGateFixture(t, "go.mod")
	err := auditAPICompatibilityGate(t.Context(), manifest, root)
	if err == nil || !strings.Contains(err.Error(), "retains Praetor asset") {
		t.Fatalf("retained gate files: %v", err)
	}
	for _, rel := range []string{apiassets.WorkflowFile, apiassets.Directory + "/" + apiassets.GateFile} {
		writeFixtureFile(t, root, rel, "# the operator's own file\n")
	}
	err = auditAPICompatibilityGate(t.Context(), manifest, root)
	if err == nil || !strings.Contains(err.Error(), "retains required context \""+apiassets.StatusContext+"\"") {
		t.Fatalf("retained ruleset context: %v", err)
	}
	if err := auditAPICompatibilityGate(t.Context(), declinedBranchRulesetManifest("docs:seo-portal"), root); err != nil {
		t.Fatalf("declined ruleset still read: %v", err)
	}
	writeFixtureFile(t, root, ".github/rulesets/main.json", "{}\n")
	if err := auditAPICompatibilityGate(t.Context(), manifest, root); err != nil {
		t.Fatalf("operator files and a ruleset without the context failed: %v", err)
	}
}

// Boundary: a repository whose only go.mod is nested gets the gate, which the audit verifies.
func TestAuditAPICompatibilityGate_Boundary_NestedModuleIsAudited(t *testing.T) {
	root := apiGateFixture(t, "lib/go.mod")
	out, err := captureStdout(t, func() error { return auditAPICompatibilityGate(t.Context(), apiGateManifest(), root) })
	if err != nil || !strings.Contains(out, "[PASS] Locked API compatibility gate verified") ||
		!strings.Contains(out, "on a draft it fails by design until the draft is marked ready") {
		t.Fatalf("nested module audit = %v:\n%s", err, out)
	}
}

// Negative: a Python repository declaring the facet, whose only go.mod is a fixture under
// testdata, needs no gate: the audit says no checker runs for its languages, neither a pass nor
// a failure. Boundary: gate files and a required context left from a Go past fail it, naming the
// adoption run that retires them.
func TestAuditAPICompatibilityGate_Negative_NoGoModuleRunsNoChecker(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, root, "pyproject.toml", "[project]\nname = \"widget\"\n")
	writeFixtureFile(t, root, "testdata/fixture/go.mod", "module example.com/fixture\n")
	ruleset, err := forge.RenderRepositoryRuleset("main", config.DefaultPolicy().BranchProtection, nil)
	if err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, root, ".github/rulesets/main.json", string(ruleset))
	initGitFixture(t, root)
	out, err := captureStdout(t, func() error { return auditAPICompatibilityGate(t.Context(), apiGateManifest(), root) })
	if err != nil || !strings.Contains(out, "[INFO] "+adopt.NoAPICompatibilityChecker+".") ||
		strings.Contains(out, "[PASS]") || strings.Contains(out, "[FAIL]") {
		t.Fatalf("audit without a Go module = %v:\n%s", err, out)
	}

	stale := apiGateFixture(t, "")
	out, err = captureStdout(t, func() error { return auditAPICompatibilityGate(t.Context(), apiGateManifest(), stale) })
	if err == nil || !strings.Contains(err.Error(), "retains Praetor asset") ||
		!strings.Contains(err.Error(), "git tracks no go.mod, so rerun praetorctl adopt") ||
		!strings.Contains(out, adopt.NoAPICompatibilityChecker) {
		t.Fatalf("stale gate without a Go module = %v:\n%s", err, out)
	}
}
