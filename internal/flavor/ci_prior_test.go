// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package flavor_test

// #817: the CI workflow templates moved to the hosted gate shape. A plain apply refreshes an
// unedited copy of any text they rendered before, in its own line-ending style, and keeps an
// edited copy until --force.

import (
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/flavor"
	"github.com/cordanaLLM/praetor/internal/util"
)

// ciWorkflowPath is where every flavor scaffolds its CI workflow.
const ciWorkflowPath = ".github/workflows/ci.yml"

// ciPriorFixtures holds every CI workflow text earlier releases scaffolded, one directory per
// template directory under templates/ and one file per text.
const ciPriorFixtures = "testdata/ci-prior"

// ciWorkflowFlavors maps each flavor that scaffolds a CI workflow to its template's directory
// under templates/, which names its fixtures under ciPriorFixtures.
var ciWorkflowFlavors = map[string]string{
	"go-service": "go", "go-library": "go", "rust-systems": "rust", "typescript-node": "node",
	"jvm-service": "jvm", "mobile-flutter": "flutter",
}

// ciWorkflowTemplate is the CI workflow template a flavor scaffolds, and whether it has one.
func ciWorkflowTemplate(flv flavor.Flavor) (flavor.TemplateItem, bool) {
	for _, tmpl := range flv.RequiredTemplates() {
		if tmpl.Path == ciWorkflowPath && strings.Contains(tmpl.Source, "/ci-") {
			return tmpl, true
		}
	}
	return flavor.TemplateItem{}, false
}

// ciPriors returns the CI template a flavor scaffolds and the earlier texts of it, by fixture
// name, in LF.
func ciPriors(t *testing.T, name string) (flavor.TemplateItem, map[string]string) {
	t.Helper()
	flv, err := flavor.Get(name)
	if err != nil {
		t.Fatal(err)
	}
	tmpl, ok := ciWorkflowTemplate(flv)
	if !ok {
		t.Fatalf("%s scaffolds no CI workflow", name)
	}
	return tmpl, readPriorFixtures(t, path.Join(ciPriorFixtures, ciWorkflowFlavors[name]))
}

// applyOver applies flavor name, without --force unless force, to a repository holding its
// prerequisites and ci.yml holding before, and returns the report and the ci.yml it leaves.
func applyOver(t *testing.T, name, before string, force bool) (*flavor.ApplyReport, string) {
	t.Helper()
	repo := flavorRepo(t, name)
	workflow := filepath.Join(repo, filepath.FromSlash(ciWorkflowPath))
	if err := os.MkdirAll(filepath.Dir(workflow), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(workflow, []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}
	report, err := flavor.ApplyFlavor(t.Context(), repo, name, force)
	if err != nil {
		t.Fatalf("apply %s: %v", name, err)
	}
	after, err := os.ReadFile(workflow)
	if err != nil {
		t.Fatal(err)
	}
	return report, string(after)
}

// Every flavor that scaffolds a CI workflow template records its earlier texts, the fixtures and
// the recorded digests are one set, and boundary: the current rendering is not among them, so a
// recorded text is always one a refresh changes.
func TestCIWorkflowPriorTextsAreRecorded(t *testing.T) {
	for _, flv := range flavor.List() {
		if _, ok := ciWorkflowTemplate(flv); ok && ciWorkflowFlavors[flv.Name()] == "" {
			t.Errorf("%s scaffolds a CI workflow template this test does not read", flv.Name())
		}
	}
	for _, name := range slices.Sorted(maps.Keys(ciWorkflowFlavors)) {
		tmpl, fixtures := ciPriors(t, name)
		if dir := ciWorkflowFlavors[name]; path.Dir(tmpl.Source) != dir {
			t.Fatalf("%s renders %s, not a template under templates/%s", name, tmpl.Source, dir)
		}
		assertPriorFixturesRecorded(t, tmpl.Prior, fixtures, path.Join(ciPriorFixtures, ciWorkflowFlavors[name]))
		current := scaffoldInto(t, name, ciWorkflowPath)[ciWorkflowPath]
		if _, known, _ := util.LookupCanonicalText([]byte(current), tmpl.Prior); known {
			t.Errorf("%s records its current CI workflow rendering as an earlier text", name)
		}
	}
}

// Positive: an unedited copy of every earlier CI workflow text is refreshed to the current
// rendering without --force, in its own line-ending style, and reported refreshed. A Node text
// of another package manager is Praetor output too, and takes the job of the manager the
// repository uses.
func TestCIWorkflowApply_Positive_RefreshesAnEarlierText(t *testing.T) {
	for _, name := range slices.Sorted(maps.Keys(ciWorkflowFlavors)) {
		_, fixtures := ciPriors(t, name)
		current := scaffoldInto(t, name, ciWorkflowPath)[ciWorkflowPath]
		for _, fixture := range slices.Sorted(maps.Keys(fixtures)) {
			for _, crlf := range []bool{false, true} {
				report, got := applyOver(t, name, util.RestoreLineEndings(fixtures[fixture], crlf), false)
				if want := util.RestoreLineEndings(current, crlf); got != want {
					t.Errorf("%s over %s (crlf %v): ci.yml was not refreshed to the current rendering:\n%s", name, fixture, crlf, got)
				}
				if !slices.Contains(report.RefreshedTemplates, ciWorkflowPath) || slices.Contains(report.SkippedTemplates, ciWorkflowPath) {
					t.Errorf("%s over %s (crlf %v): refreshed %v, skipped %v", name, fixture, crlf, report.RefreshedTemplates, report.SkippedTemplates)
				}
			}
		}
	}
}

// Negative: an edited copy of an earlier text, and boundary: one whose line endings are mixed,
// are kept without --force and reported skipped; --force replaces the edited one.
func TestCIWorkflowApply_Negative_KeepsAnEditedCopy(t *testing.T) {
	for _, name := range slices.Sorted(maps.Keys(ciWorkflowFlavors)) {
		_, fixtures := ciPriors(t, name)
		earlier := fixtures[slices.Sorted(maps.Keys(fixtures))[0]]
		edited := earlier + "# tuned by the repository\n"
		mixed := strings.Replace(earlier, "\n", "\r\n", 1)
		for label, before := range map[string]string{"edited": edited, "mixed line endings": mixed} {
			report, got := applyOver(t, name, before, false)
			if got != before || slices.Contains(report.RefreshedTemplates, ciWorkflowPath) || !slices.Contains(report.SkippedTemplates, ciWorkflowPath) {
				t.Errorf("%s over an %s copy: kept %v, refreshed %v, skipped %v", name, label, got == before, report.RefreshedTemplates, report.SkippedTemplates)
			}
		}
		report, got := applyOver(t, name, edited, true)
		if want := scaffoldInto(t, name, ciWorkflowPath)[ciWorkflowPath]; got != want || !slices.Contains(report.CreatedTemplates, ciWorkflowPath) {
			t.Errorf("%s --force over an edited copy: replaced %v, created %v", name, got == want, report.CreatedTemplates)
		}
	}
}
