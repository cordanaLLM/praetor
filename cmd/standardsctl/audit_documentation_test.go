package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/adopt"
	"github.com/cordanaLLM/praetor/internal/baseline"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/readmegovernance"
	markdownassets "github.com/cordanaLLM/praetor/tools/markdownlint"
)

func TestAuditDocumentationGatePositive(t *testing.T) {
	root := documentationAuditFixture(t)
	manifest := &config.Manifest{Facets: []string{"docs:seo-portal"}}
	if err := docGate(t.Context(), manifest, root); err != nil {
		t.Fatal(err)
	}
}

func TestAuditDocumentationGateCRLF(t *testing.T) {
	root := documentationAuditFixture(t)
	paths := append(adopt.DocumentationAssetPaths(), "Makefile")
	for _, rel := range paths {
		path := filepath.Join(root, filepath.FromSlash(rel))
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		writeFixtureFile(t, root, rel, strings.ReplaceAll(string(data), "\n", "\r\n"))
	}
	manifest := &config.Manifest{Facets: []string{"docs:seo-portal"}}
	if err := docGate(t.Context(), manifest, root); err != nil {
		t.Fatalf("consistent CRLF documentation surfaces failed audit: %v", err)
	}
}

func TestAuditDocumentationGateNegative(t *testing.T) {
	for name, mutate := range map[string]func(*testing.T, string){
		"asset drift": func(t *testing.T, root string) {
			writeFixtureFile(t, root, "tools/markdownlint/package.json", "{}\n")
		},
		"workflow drift": func(t *testing.T, root string) {
			writeFixtureFile(t, root, adopt.DocumentationWorkflowFile, "name: incomplete\n")
		},
		"asset mixed endings": func(t *testing.T, root string) {
			path := filepath.Join(root, "tools/markdownlint/package.json")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			writeFixtureFile(t, root, "tools/markdownlint/package.json",
				strings.Replace(string(data), "\n", "\r\n", 1))
		},
		"workflow mixed endings": func(t *testing.T, root string) {
			writeFixtureFile(t, root, adopt.DocumentationWorkflowFile,
				strings.Replace(adopt.DocumentationWorkflow(), "\n", "\r\n", 1))
		},
		"Makefile mixed endings": func(t *testing.T, root string) {
			writeFixtureFile(t, root, "Makefile",
				strings.Replace(adopt.DocumentationMakefileBlock(), "\n", "\r\n", 1))
		},
		"missing Makefile wiring": func(t *testing.T, root string) {
			writeFixtureFile(t, root, "Makefile", "verify-all:\n\t@true\n")
		},
		"missing second scratch ignore": func(t *testing.T, root string) {
			writeFixtureFile(t, root, ".gitignore", "/.workingdir/\n")
		},
		"stale formatter inventory": func(t *testing.T, root string) {
			writeFixtureFile(t, root, adopt.FormatterIgnoreFile, adopt.ManagedFormatterIgnoreBlock(false))
		},
		"missing required formatter inventory": func(t *testing.T, root string) {
			writeFixtureFile(t, root, ".prettierrc", "{}\n")
		},
		"leading-space scratch patterns": func(t *testing.T, root string) {
			writeFixtureFile(t, root, ".gitignore", " /.workingdir/\n /.workingdir2/\n")
		},
		"later scratch negations": func(t *testing.T, root string) {
			writeFixtureFile(t, root, ".gitignore", "/.workingdir/\n/.workingdir2/\n"+
				"!/.workingdir/\n!/.workingdir/**\n!/.workingdir2/\n!/.workingdir2/**\n")
		},
		"missing required context": func(t *testing.T, root string) {
			path := filepath.Join(root, ".github", "rulesets", "main.json")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			writeFixtureFile(t, root, ".github/rulesets/main.json",
				strings.Replace(string(data), adopt.DocumentationStatusContext, "Different Context", 1))
		},
	} {
		t.Run(name, func(t *testing.T) {
			root := documentationAuditFixture(t)
			mutate(t, root)
			err := docGate(t.Context(), &config.Manifest{Facets: []string{"docs:seo-portal"}}, root)
			if err == nil {
				t.Fatal("documentation drift passed audit")
			}
		})
	}
}

// An earlier Praetor text of the workflow fails the byte lock like any drift, but names plain
// adoption, which refreshes it, where an edited copy names --force; with the facet disabled
// the earlier text still counts as a retained Praetor asset.
func TestAuditDocumentationGatePriorText(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", markdownassets.Directory, "testdata", "prior", "praetor-docs.ubuntu-26.04-v4.yml"))
	if err != nil {
		t.Fatal(err)
	}
	// A Windows checkout may convert testdata to CRLF; the edited case must not mix endings.
	prior := []byte(strings.ReplaceAll(string(raw), "\r\n", "\n"))
	enabled := &config.Manifest{Facets: []string{"docs:seo-portal"}}
	for text, want := range map[string]string{
		string(prior):                                   "holds an earlier Praetor text; run 'praetorctl adopt' to refresh it",
		string(prior) + "# operator\n":                  "differs from the locked Praetor asset; run 'praetorctl adopt --force'",
		strings.ReplaceAll(string(prior), "\n", "\r\n"): "holds an earlier Praetor text",
	} {
		root := documentationAuditFixture(t)
		writeFixtureFile(t, root, adopt.DocumentationWorkflowFile, text)
		if err := docGate(t.Context(), enabled, root); err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("workflow %q: audit = %v, want %q", text[:20], err, want)
		}
	}
	root := t.TempDir()
	writeFixtureFile(t, root, adopt.DocumentationWorkflowFile, string(prior))
	if err := docGate(t.Context(), &config.Manifest{}, root); err == nil || !strings.Contains(err.Error(), "retains Praetor asset") {
		t.Fatalf("disabled facet over an earlier workflow text: %v", err)
	}
}

// Positive: a declared documentation block is recorded on its own audit line with the effective
// bounds and every glob. Negative: a manifest without the block records nothing past the gate
// line. Boundary: a bound exactly at its ceiling is recorded as declared, and an empty block
// records the defaults and no exclusions.
func TestAuditDocumentationGateRecordsDeclaredSettings(t *testing.T) {
	root := documentationAuditFixture(t)
	maxFiles := config.DocumentationMaxFilesCeiling
	facets := []string{"docs:seo-portal"}
	for name, tc := range map[string]struct {
		policy *config.DocumentationPolicy
		want   string
	}{
		"declared": {
			&config.DocumentationPolicy{MaxFiles: &maxFiles, StyleExclude: []string{"changelog.d/**", "docs/generated/*.md"}},
			"[PASS] Documentation gate settings from .standards.yaml: max_files 16384, max_file_bytes 1048576, " +
				"2 style exclusions (changelog.d/**, docs/generated/*.md).\n",
		},
		"empty block": {
			&config.DocumentationPolicy{},
			"[PASS] Documentation gate settings from .standards.yaml: max_files 4096, max_file_bytes 1048576, no style exclusions.\n",
		},
		"absent block": {nil, ""},
	} {
		t.Run(name, func(t *testing.T) {
			manifest := &config.Manifest{Facets: facets, Documentation: tc.policy}
			out, err := captureStdout(t, func() error { return docGate(t.Context(), manifest, root) })
			if err != nil {
				t.Fatal(err)
			}
			if tc.want == "" && strings.Contains(out, "Documentation gate settings") {
				t.Fatalf("audit recorded settings no manifest declared:\n%s", out)
			}
			if tc.want != "" && !strings.Contains(out, tc.want) {
				t.Fatalf("audit output lacks %q:\n%s", tc.want, out)
			}
		})
	}
}

func TestAuditDocumentationGateBoundary(t *testing.T) {
	if err := docGate(t.Context(), &config.Manifest{}, t.TempDir()); err != nil {
		t.Fatalf("repository without documentation facet was gated: %v", err)
	}
}

func TestAuditDocumentationGateDisabledRejectsStaleSurfaces(t *testing.T) {
	for name, seed := range map[string]func(*testing.T, string){
		"workflow": func(t *testing.T, root string) {
			writeFixtureFile(t, root, adopt.DocumentationWorkflowFile, adopt.DocumentationWorkflow())
		},
		"tool asset": func(t *testing.T, root string) {
			data, err := markdownassets.Read("package.json")
			if err != nil {
				t.Fatal(err)
			}
			writeFixtureFile(t, root, "tools/markdownlint/package.json", string(data))
		},
		"Makefile block": func(t *testing.T, root string) {
			writeFixtureFile(t, root, "Makefile", adopt.DocumentationMakefileBlock())
		},
		"ruleset context": func(t *testing.T, root string) {
			writeFixtureFile(t, root, ".github/rulesets/main.json",
				`{"rules":[{"type":"required_status_checks","parameters":{"required_status_checks":[{"context":"`+
					adopt.DocumentationStatusContext+`"}]}}]}`)
		},
		"formatter inventory": func(t *testing.T, root string) {
			writeFixtureFile(t, root, adopt.FormatterIgnoreFile, adopt.ManagedFormatterIgnoreBlock(true))
		},
		"missing formatter inventory": func(t *testing.T, root string) {
			writeFixtureFile(t, root, ".prettierrc", "{}\n")
		},
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			seed(t, root)
			if err := docGate(t.Context(), &config.Manifest{}, root); err == nil {
				t.Fatal("disabled documentation facet accepted a stale Praetor surface")
			}
		})
	}
}

func TestAuditDocumentationGateDisabledAllowsOperatorLookalikes(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, root, "tools/markdownlint/package.json", "{\"operator\":true}\n")
	writeFixtureFile(t, root, adopt.DocumentationWorkflowFile,
		"name: Operator docs\non: workflow_dispatch\njobs: {}\n")
	writeFixtureFile(t, root, "Makefile",
		"# prose mentions "+"# BEGIN praetor documentation gate"+" but owns no marker line\noperator:\n\t@true\n")
	writeFixtureFile(t, root, ".github/rulesets/main.json",
		`{"metadata":{"example":"`+adopt.DocumentationStatusContext+`"},"rules":[]}`)
	if err := docGate(t.Context(), &config.Manifest{}, root); err != nil {
		t.Fatalf("disabled facet rejected operator-owned lookalikes: %v", err)
	}
}

func declinedBranchRulesetManifest(facets ...string) *config.Manifest {
	return declinedDocumentationManifest([]string{"branch-ruleset"}, facets...)
}

func declinedDocumentationManifest(declines []string, facets ...string) *config.Manifest {
	return &config.Manifest{
		Facets:   facets,
		Adoption: &config.AdoptionPolicy{Decline: declines},
	}
}

// operatorOwnedDocumentationStep is a declinable adoption step whose surface the documentation
// gate reads, with the Praetor bytes adoption would have written there made stale.
type operatorOwnedDocumentationStep struct {
	name    string
	step    string
	enabled bool
	stale   func(*testing.T, string)
}

func operatorOwnedDocumentationSteps() []operatorOwnedDocumentationStep {
	return []operatorOwnedDocumentationStep{
		{name: "enabled makefile", step: "makefile", enabled: true, stale: func(t *testing.T, root string) {
			writeFixtureFile(t, root, "Makefile", "verify-all:\n\t@true\n")
		}},
		{name: "enabled git-ignore", step: "git-ignore", enabled: true, stale: func(t *testing.T, root string) {
			writeFixtureFile(t, root, ".gitignore", "# operator rules\n/.workingdir/\n/.workingdir2/\n")
		}},
		{name: "enabled formatter-ignore", step: "formatter-ignore", enabled: true, stale: func(t *testing.T, root string) {
			writeFixtureFile(t, root, ".prettierrc", "{}\n")
			writeFixtureFile(t, root, adopt.FormatterIgnoreFile, adopt.ManagedFormatterIgnoreBlock(false))
		}},
		{name: "disabled makefile", step: "makefile", stale: func(t *testing.T, root string) {
			writeFixtureFile(t, root, "Makefile", adopt.DocumentationMakefileBlock())
		}},
		{name: "disabled formatter-ignore", step: "formatter-ignore", stale: func(t *testing.T, root string) {
			writeFixtureFile(t, root, adopt.FormatterIgnoreFile, adopt.ManagedFormatterIgnoreBlock(true))
		}},
	}
}

func (step operatorOwnedDocumentationStep) fixture(t *testing.T) (string, []string) {
	t.Helper()
	if !step.enabled {
		root := t.TempDir()
		step.stale(t, root)
		return root, nil
	}
	root := documentationAuditFixture(t)
	step.stale(t, root)
	return root, []string{"docs:seo-portal"}
}

// Positive: adoption skips a declined step (#408), so the bytes it would have written are
// operator-owned. Audit must accept whatever the operator keeps there instead of demanding
// Praetor bytes that rerunning adopt can never restore.
func TestAuditDocumentationGateHonorsOperatorOwnedDeclines(t *testing.T) {
	for _, step := range operatorOwnedDocumentationSteps() {
		t.Run(step.name, func(t *testing.T) {
			root, facets := step.fixture(t)
			manifest := declinedDocumentationManifest([]string{step.step}, facets...)
			if err := docGate(t.Context(), manifest, root); err != nil {
				t.Fatalf("declined %s surface failed audit: %v", step.step, err)
			}
		})
	}
}

// Negative: the same stale surface still fails while the step is not declined, including when
// the manifest declines a different step; a decline covers its own step only.
func TestAuditDocumentationGateUndeclinedStepsStayFailClosed(t *testing.T) {
	for _, step := range operatorOwnedDocumentationSteps() {
		t.Run(step.name, func(t *testing.T) {
			root, facets := step.fixture(t)
			for _, declines := range [][]string{nil, {"readme"}} {
				manifest := declinedDocumentationManifest(declines, facets...)
				if err := docGate(t.Context(), manifest, root); err == nil {
					t.Fatalf("stale %s surface passed audit with declines %v", step.step, declines)
				}
			}
		})
	}
}

// Boundary: a decline resolves exactly as adoption resolves it. Case and surrounding space
// canonicalise; an unknown, mandatory, or oversized decline list fails closed even when it
// also names the step.
func TestAuditDocumentationGateDeclineBoundary(t *testing.T) {
	for _, step := range operatorOwnedDocumentationSteps() {
		t.Run(step.name, func(t *testing.T) {
			root, facets := step.fixture(t)
			spelled := declinedDocumentationManifest([]string{"  " + strings.ToUpper(step.step) + " "}, facets...)
			if err := docGate(t.Context(), spelled, root); err != nil {
				t.Fatalf("canonicalised %s decline was not honored: %v", step.step, err)
			}
			for _, declines := range [][]string{
				{step.step, "not-an-artifact"},
				{step.step, "documentation-gate"},
				slices.Repeat([]string{step.step}, 65),
			} {
				manifest := declinedDocumentationManifest(declines, facets...)
				if err := docGate(t.Context(), manifest, root); err == nil {
					t.Fatalf("malformed decline list %v did not fail closed", declines[:2])
				}
			}
		})
	}
}

// Negative: declining git-ignore hands the file to the operator, not the privacy invariant.
// Both scratch roots must still be effectively ignored.
func TestAuditDocumentationGateGitIgnoreDeclineKeepsScratchPrivacy(t *testing.T) {
	for name, ignore := range map[string]string{
		"missing second root": "/.workingdir/\n",
		"later negation":      "/.workingdir/\n/.workingdir2/\n!/.workingdir2/\n!/.workingdir2/**\n",
		"absent":              "",
	} {
		t.Run(name, func(t *testing.T) {
			root := documentationAuditFixture(t)
			if ignore == "" {
				if err := os.Remove(filepath.Join(root, ".gitignore")); err != nil {
					t.Fatal(err)
				}
			} else {
				writeFixtureFile(t, root, ".gitignore", ignore)
			}
			manifest := declinedDocumentationManifest([]string{"git-ignore"}, "docs:seo-portal")
			err := docGate(t.Context(), manifest, root)
			if err == nil || !strings.Contains(err.Error(), "effectively exclude") {
				t.Fatalf("declined git-ignore excused unignored private scratch: %v", err)
			}
		})
	}
}

// Boundary: only the repository's own ignore files prove scratch privacy. A global excludes
// file hides .workingdir2 on this machine alone, so it must not satisfy the audit.
func TestAuditDocumentationGateIgnoresOperatorGlobalExcludes(t *testing.T) {
	home := t.TempDir()
	excludes := filepath.Join(home, "excludes")
	writeFixtureFile(t, home, "excludes", "/.workingdir2/\n")
	writeFixtureFile(t, home, "gitconfig", "[core]\n\texcludesFile = "+filepath.ToSlash(excludes)+"\n")
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(home, "gitconfig"))

	root := documentationAuditFixture(t)
	writeFixtureFile(t, root, ".gitignore", "/.workingdir/\n")
	manifest := declinedDocumentationManifest([]string{"git-ignore"}, "docs:seo-portal")
	err := docGate(t.Context(), manifest, root)
	if err == nil || !strings.Contains(err.Error(), ".workingdir2/PRAETOR-AUDIT-PROBE") {
		t.Fatalf("a personal excludes file proved repository scratch privacy: %v", err)
	}
}

func TestAuditDocumentationGateHonorsBranchRulesetDecline(t *testing.T) {
	root := documentationAuditFixture(t)
	if err := os.Remove(filepath.Join(root, ".github", "rulesets", "main.json")); err != nil {
		t.Fatal(err)
	}
	if err := docGate(t.Context(), declinedBranchRulesetManifest("docs:seo-portal"), root); err != nil {
		t.Fatalf("declined branch ruleset still required the documentation context: %v", err)
	}
	err := docGate(t.Context(), &config.Manifest{Facets: []string{"docs:seo-portal"}}, root)
	if err == nil || !strings.Contains(err.Error(), "branch ruleset") {
		t.Fatalf("missing ruleset without a decline passed or failed for another reason: %v", err)
	}
}

func TestAuditDocumentationGateBranchRulesetDeclineKeepsWorkflowContext(t *testing.T) {
	root := documentationAuditFixture(t)
	writeFixtureFile(t, root, adopt.DocumentationWorkflowFile,
		strings.Replace(adopt.DocumentationWorkflow(), "name: "+adopt.DocumentationStatusContext, "name: Other", 1))
	if err := docGate(t.Context(), declinedBranchRulesetManifest("docs:seo-portal"), root); err == nil {
		t.Fatal("branch-ruleset decline excused a workflow that no longer reports the documentation context")
	}
}

func TestAuditDocumentationGateBranchRulesetDeclineBoundary(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, root, ".github/rulesets/main.json",
		`{"rules":[{"type":"required_status_checks","parameters":{"required_status_checks":[{"context":"`+
			adopt.DocumentationStatusContext+`"}]}}]}`)
	if err := docGate(t.Context(), declinedBranchRulesetManifest(), root); err != nil {
		t.Fatalf("disabled facet claimed an operator-owned declined ruleset: %v", err)
	}
	for _, decline := range []string{"not-an-artifact", "documentation-gate"} {
		invalid := &config.Manifest{Adoption: &config.AdoptionPolicy{Decline: []string{decline}}}
		if err := docGate(t.Context(), invalid, root); err == nil {
			t.Fatalf("invalid decline %q did not fail closed", decline)
		}
	}
}

func TestAuditReadmeDocumentationContract(t *testing.T) {
	manifest := &config.Manifest{
		Repository: config.RepositoryMetadata{Owner: "acme", Name: "widgets"},
		Facets:     []string{"docs:seo-portal"},
	}
	state := readmegovernance.State{
		BaselineKnown:        true,
		DocumentationEnabled: true,
		RepositoryOwner:      "acme",
		RepositoryName:       "widgets",
	}
	canonical, _, err := readmegovernance.Reconcile("# Widgets\n", state)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	opts := &auditOptions{
		rootDir: root, baseline: &baseline.Baseline{Version: 1}, baselineKnown: true,
	}
	writeFixtureFile(t, root, "README.md", canonical)
	if err := auditReadmeGovernance(t.Context(), manifest, opts); err != nil {
		t.Fatalf("canonical documentation README failed audit: %v", err)
	}
	if err := auditReadmeGovernance(t.Context(), &config.Manifest{Repository: manifest.Repository}, opts); err == nil {
		t.Fatal("documentation-disabled README retained the enabled badge and verification row")
	}
	for name, stale := range map[string]string{
		"missing badge": strings.Replace(canonical,
			"[![Documentation Governance][praetor-docs-badge]][praetor-docs-runs]\n", "", 1),
		"stale badge": strings.Replace(canonical, "github.com/acme/widgets/", "github.com/acme/old-widgets/", 1),
		"missing row": strings.Replace(canonical,
			"**Documentation**: `make docs-lint` enforces locked Markdown style and the\n", "", 1),
	} {
		t.Run(name, func(t *testing.T) {
			writeFixtureFile(t, root, "README.md", stale)
			if err := auditReadmeGovernance(t.Context(), manifest, opts); err == nil {
				t.Fatal("stale documentation README passed audit")
			}
		})
	}
}

// docGate runs the gate under the branch protection an unlocked fixture resolves to:
// built-in defaults plus the manifest's overrides.
func docGate(ctx context.Context, manifest *config.Manifest, root string) error {
	policy := config.DefaultPolicy()
	policy.ApplyOverrides(manifest.Overrides)
	return auditDocumentationGate(ctx, manifest, root, policy.BranchProtection)
}

// The gate compares the ruleset with the branch protection the audit resolved, which now
// includes what the pinned profiles and facets require, not with the built-in defaults.
func TestAuditDocumentationGateUsesTheEffectiveBranchProtection(t *testing.T) {
	root := documentationAuditFixture(t)
	manifest := &config.Manifest{Facets: []string{"docs:seo-portal"}}
	joined := config.DefaultPolicy().BranchProtection
	joined.RequireSignedCommits, joined.RequiredApprovingReviewers = true, 2
	// Negative: a ruleset rendered from the defaults does not satisfy the joined policy.
	if err := auditDocumentationGate(t.Context(), manifest, root, joined); err == nil {
		t.Fatal("ruleset rendered from the defaults satisfied a stricter effective policy")
	}
	// Positive: the ruleset adopt renders from the joined policy passes.
	ruleset, err := forge.RenderRepositoryRuleset("main", joined, []string{adopt.DocumentationStatusContext})
	if err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, root, ".github/rulesets/main.json", string(ruleset))
	if err := auditDocumentationGate(t.Context(), manifest, root, joined); err != nil {
		t.Fatalf("ruleset rendered from the effective policy failed: %v", err)
	}
	// Boundary: the defaults no longer match once the ruleset follows the joined policy.
	if err := docGate(t.Context(), manifest, root); err == nil {
		t.Fatal("defaults matched a ruleset rendered from a stricter policy")
	}
}

func documentationAuditFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, name := range markdownassets.Names() {
		data, err := markdownassets.Read(name)
		if err != nil {
			t.Fatal(err)
		}
		writeFixtureFile(t, root, filepath.Join(markdownassets.Directory, name), string(data))
	}
	writeFixtureFile(t, root, adopt.DocumentationWorkflowFile, adopt.DocumentationWorkflow())
	writeFixtureFile(t, root, "Makefile", adopt.DocumentationMakefileBlock())
	writeFixtureFile(t, root, ".gitignore", adopt.ManagedGitIgnoreBlock())
	if !strings.Contains(adopt.DocumentationWorkflow(), "name: "+adopt.DocumentationStatusContext) {
		t.Fatal("fixture workflow lacks its required status context")
	}
	ruleset, err := forge.RenderRepositoryRuleset("main", config.DefaultPolicy().BranchProtection,
		[]string{adopt.DocumentationStatusContext})
	if err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, root, ".github/rulesets/main.json", string(ruleset))
	initGitFixture(t, root)
	return root
}
