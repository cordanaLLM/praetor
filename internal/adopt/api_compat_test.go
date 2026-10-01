// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/forge"
	apiassets "github.com/cordanaLLM/praetor/tools/apicompat"
)

// apiGatePaths are the files the api:public-contract facet owns, spelled out rather than read
// from the code under test.
var apiGatePaths = []string{".github/workflows/praetor-api.yml", "tools/apicompat/gate/main.go"}

func assertAPIGate(t *testing.T, root string, present bool) {
	t.Helper()
	for _, rel := range apiGatePaths {
		_, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel)))
		if present != (err == nil) {
			t.Fatalf("%s present=%v, want %v (%v)", rel, err == nil, present, err)
		}
	}
	if !present {
		return
	}
	if got := mustRead(t, filepath.Join(root, filepath.FromSlash(apiGatePaths[0]))); got != apiassets.Workflow {
		t.Fatal("the emitted workflow is not the locked text")
	}
}

func rulesetRequiresAPIGate(t *testing.T, root string) bool {
	t.Helper()
	required, err := forge.RulesetRequiresStatusContext([]byte(mustRead(t, filepath.Join(root, rulesetFile))), APICompatibilityStatusContext)
	if err != nil {
		t.Fatal(err)
	}
	return required
}

// Positive: the default facets emit the gate, its workflow joins the rendered ruleset and the
// actionlint labels, and a rerun changes nothing. Negative: removing the facet without --force
// stops while the ruleset requires the context, before the gate or the context goes. Boundary:
// with --force the gate goes and the ruleset follows; re-adding the facet brings both back.
func TestAdoptionAPICompatibilityGateFacetTransitionConverges(t *testing.T) {
	root := newTestRepo(t, "api-gate-transition")
	mustWrite(t, filepath.Join(root, "go.mod"), "module example.com/widget\n\ngo 1.27\n")
	opts := AdoptOptions{Path: root, Profile: "framework", LockSourceRoot: newAdoptLockSource(t)}
	if _, err := Adopt(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
	assertAPIGate(t, root, true)
	if !rulesetRequiresAPIGate(t, root) {
		t.Fatal("the rendered ruleset does not require the API compatibility context")
	}
	if actionlint := mustRead(t, filepath.Join(root, ".github", "actionlint.yaml")); !strings.Contains(actionlint, "ubuntu-26.04") {
		t.Fatalf("actionlint configuration lacks the gate's runner label:\n%s", actionlint)
	}
	enabled := snapshotTree(t, root)
	if _, err := Adopt(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
	assertTreeUnchanged(t, enabled, snapshotTree(t, root))

	setManifestFacet(t, root, "api:public-contract", false)
	if _, err := Adopt(t.Context(), opts); err == nil || !strings.Contains(err.Error(), "removes hosted context \"Go API Compatibility\"") {
		t.Fatalf("unforced disable = %v, want the hosted context refusal", err)
	}
	assertAPIGate(t, root, true)
	if !rulesetRequiresAPIGate(t, root) {
		t.Fatal("a refused disable dropped the context from the ruleset")
	}
	opts.Force = true
	if _, err := Adopt(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
	assertAPIGate(t, root, false)
	if rulesetRequiresAPIGate(t, root) {
		t.Fatal("the ruleset still requires the removed gate's context")
	}

	// The lock no longer pins the facet once it was removed, so re-adding it re-pins with --force,
	// as for any facet; a plain run then converges.
	setManifestFacet(t, root, "api:public-contract", true)
	if _, err := Adopt(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
	assertAPIGate(t, root, true)
	if !rulesetRequiresAPIGate(t, root) {
		t.Fatal("the re-enabled gate's context is not required")
	}
	reenabled := snapshotTree(t, root)
	opts.Force = false
	if _, err := Adopt(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
	assertTreeUnchanged(t, reenabled, snapshotTree(t, root))
}

// Negative: a file the repository already had at a managed path is not overwritten, even under
// --force, before the gate was ever adopted. Boundary: the facet's off switch is the facet, so
// adoption.decline cannot name the step.
func TestAdoptionAPICompatibilityGateRefusals(t *testing.T) {
	root := newTestRepo(t, "api-gate-foreign")
	foreign := filepath.Join(root, "tools", "apicompat", "gate", "main.go")
	mustWrite(t, foreign, "package main\n\nfunc main() {}\n")
	opts := AdoptOptions{Path: root, Profile: "framework", LockSourceRoot: newAdoptLockSource(t), Force: true}
	if _, err := Adopt(t.Context(), opts); err == nil || !strings.Contains(err.Error(), "refusing to adopt the API compatibility family over tools/apicompat/gate/main.go") {
		t.Fatalf("adopt over a foreign gate file = %v", err)
	}
	if got := mustRead(t, foreign); got != "package main\n\nfunc main() {}\n" {
		t.Fatalf("foreign gate file changed: %q", got)
	}
	if _, err := ArtifactDeclined([]string{"api-compatibility-gate"}, "branch-ruleset"); err == nil ||
		!strings.Contains(err.Error(), "remove the api:public-contract facet instead") {
		t.Fatalf("declining the gate step = %v, want the facet switch named", err)
	}
}

// Positive: the facet enables the gate. Negative: another facet does not. Boundary: an
// inventory past the manifest bound is refused, and the facet's families are the gate alone.
func TestAPICompatibilityEnabled(t *testing.T) {
	if enabled, err := APICompatibilityEnabled([]string{"docs:seo-portal", "api:public-contract"}); err != nil || !enabled {
		t.Fatalf("declared facet: enabled=%v err=%v", enabled, err)
	}
	if enabled, err := APICompatibilityEnabled([]string{"docs:seo-portal", "api:public"}); err != nil || enabled {
		t.Fatalf("other facets: enabled=%v err=%v", enabled, err)
	}
	if _, err := APICompatibilityEnabled(make([]string, config.MaxManifestEntriesPerKind+1)); err == nil {
		t.Fatal("a facet inventory past the manifest bound was resolved")
	}
	families := APICompatibilityFamilies()
	if len(families) != 1 || families[0].WorkflowFile != APICompatibilityWorkflowFile || families[0].StatusContext != APICompatibilityStatusContext {
		t.Fatalf("api:public-contract families = %+v", families)
	}
}

// Positive and boundary: the enabled families follow the declared facets in registry order,
// none for no facet, and a blank entry enables nothing. Negative: a facet inventory past the
// manifest bound is refused rather than read in part.
func TestEnabledManagedFamilies(t *testing.T) {
	names := func(facets ...string) []string {
		t.Helper()
		families, err := EnabledManagedFamilies(facets)
		if err != nil {
			t.Fatal(err)
		}
		got := make([]string, 0, len(families))
		for _, family := range families {
			got = append(got, family.Name)
		}
		return got
	}
	if got := names("api:public-contract", "docs:seo-portal"); !slices.Equal(got, []string{"Markdown", "Figure engine", "API compatibility"}) {
		t.Fatalf("both facets enable %q", got)
	}
	if got := names("api:public-contract"); !slices.Equal(got, []string{"API compatibility"}) {
		t.Fatalf("api:public-contract enables %q", got)
	}
	if got := names(); len(got) != 0 {
		t.Fatalf("no facet enables %q", got)
	}
	if got := names("", "custom:facet"); len(got) != 0 {
		t.Fatalf("a blank and an unmanaged facet enable %q", got)
	}
	if _, err := EnabledManagedFamilies(make([]string, config.MaxManifestEntriesPerKind+1)); err == nil {
		t.Fatal("a facet inventory past the manifest bound was resolved")
	}
}
