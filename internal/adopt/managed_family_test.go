// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/cordanaLLM/praetor/internal/classify"
	"github.com/cordanaLLM/praetor/internal/managedasset"
)

// secondFamily is a family shaped like the figure engine that will join docs:seo-portal:
// nested assets, its own workflow, and refuse-on-first-adopt on. It runs through the same
// code the Markdown family does, which is the registry's claim.
func secondFamily() managedasset.Family {
	return managedasset.Family{
		Name: "fixture engine", Kind: "documentation", AssetNoun: "fixture engine asset",
		WorkflowNoun: "fixture engine workflow", Facet: managedasset.DocumentationFacet,
		Directory: "tools/fixture", Source: "tools/fixture/assets.go",
		FS: fstest.MapFS{
			"core.mjs":                {Data: []byte("export const core = 1;\n")},
			"third_party/lib/LICENSE": {Data: []byte("MIT\n")},
		},
		Assets: []string{"core.mjs", "third_party/lib/LICENSE"}, MaxAssets: 2,
		WorkflowFile: ".github/workflows/fixture.yml", StatusContext: "Fixture Gate",
		Workflow: "name: Fixture Gate\n", RefuseForeign: true,
	}
}

func familySession(t *testing.T, force bool) *adoptSession {
	t.Helper()
	root := t.TempDir()
	opts := AdoptOptions{Path: root, SkipGitValidation: true, Force: force}
	return &adoptSession{
		repoPath: root, opts: opts,
		report: newAdoptionReport(root, opts, classify.Result{Archetype: "app-service"}),
	}
}

func familyFile(t *testing.T, s *adoptSession, rel string) (string, bool) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(s.repoPath, filepath.FromSlash(rel)))
	if errors.Is(err, fs.ErrNotExist) {
		return "", false
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(data), true
}

// Positive: a second family is emitted, verified on rerun, and removed on disable through the
// same functions as the Markdown family, with no family-specific code.
func TestManagedFamilySecondFamilyLifecycle(t *testing.T) {
	s := familySession(t, false)
	family := secondFamily()
	if err := reconcileManagedFamily(t.Context(), s, family); err != nil {
		t.Fatal(err)
	}
	for _, rel := range family.ManagedPaths() {
		want, _, err := family.Canonical(rel)
		if err != nil {
			t.Fatal(err)
		}
		if got, ok := familyFile(t, s, rel); !ok || got != string(want) {
			t.Fatalf("%s = %q, want %q", rel, got, want)
		}
	}
	created, ok := actionOf(s.report, ".github/workflows/fixture.yml")
	if !ok || created.Action != actionCreate || created.Details != "Scaffolded required fixture engine workflow" {
		t.Fatalf("workflow action = %+v", created)
	}
	s.report = newAdoptionReport(s.repoPath, s.opts, classify.Result{Archetype: "app-service"})
	if err := reconcileManagedFamily(t.Context(), s, family); err != nil {
		t.Fatalf("rerun over the family's own files refused: %v", err)
	}
	verified, _ := actionOf(s.report, "tools/fixture/core.mjs")
	if verified.Details != "Existing fixture engine asset preserved; audit verifies canonical text" {
		t.Fatalf("rerun action = %+v", verified)
	}
	s.report = newAdoptionReport(s.repoPath, s.opts, classify.Result{Archetype: "app-service"})
	if err := removeManagedFamilies(t.Context(), s, []managedasset.Family{family}); err != nil {
		t.Fatal(err)
	}
	for _, rel := range family.ManagedPaths() {
		if _, ok := familyFile(t, s, rel); ok {
			t.Fatalf("%s survived removal", rel)
		}
	}
	removed, _ := actionOf(s.report, "tools/fixture/third_party/lib/LICENSE")
	if removed.Details != "Removed canonical documentation asset because docs:seo-portal is disabled" {
		t.Fatalf("removal action = %+v", removed)
	}
}

// Negative: on first adoption a file that predates adoption at any managed path is refused,
// with and without --force, and nothing of the family is written.
func TestManagedFamilyRefusesForeignFilesOnFirstAdopt(t *testing.T) {
	for _, foreign := range []struct{ rel, content string }{
		{"tools/fixture/third_party/lib/LICENSE", "Apache-2.0\n"},
		{".github/workflows/fixture.yml", "name: Operator\n"},
		{"tools/fixture/core.mjs", "export const core = 1;\r\n// mixed\n"},
	} {
		for _, force := range []bool{false, true} {
			s := familySession(t, force)
			mustWrite(t, filepath.Join(s.repoPath, filepath.FromSlash(foreign.rel)), foreign.content)
			err := reconcileManagedFamily(t.Context(), s, secondFamily())
			if err == nil || !strings.Contains(err.Error(), "refusing to adopt the fixture engine family over "+foreign.rel) {
				t.Fatalf("force=%t foreign %s: %v", force, foreign.rel, err)
			}
			if got, _ := familyFile(t, s, foreign.rel); got != foreign.content {
				t.Fatalf("force=%t: the foreign file changed to %q", force, got)
			}
			for _, rel := range secondFamily().ManagedPaths() {
				if _, ok := familyFile(t, s, rel); ok && rel != foreign.rel {
					t.Fatalf("force=%t: refusal still wrote %s", force, rel)
				}
			}
		}
	}
}

// Boundary: once one managed path holds the canonical text the family is adopted, so a
// drifted file is Praetor's own: preserved without --force, regenerated with it. With the
// flag off, as for the Markdown family, a foreign file is never refused.
func TestManagedFamilyRefusalEndsOnceAdopted(t *testing.T) {
	family := secondFamily()
	s := familySession(t, false)
	mustWrite(t, filepath.Join(s.repoPath, ".github", "workflows", "fixture.yml"), family.Workflow)
	mustWrite(t, filepath.Join(s.repoPath, "tools", "fixture", "core.mjs"), "// drifted\n")
	if err := reconcileManagedFamily(t.Context(), s, family); err != nil {
		t.Fatalf("an adopted family with one drifted file was refused: %v", err)
	}
	if got, _ := familyFile(t, s, "tools/fixture/core.mjs"); got != "// drifted\n" {
		t.Fatalf("drift was overwritten without --force: %q", got)
	}
	s.opts.Force = true
	if err := reconcileManagedFamily(t.Context(), s, family); err != nil {
		t.Fatal(err)
	}
	if got, _ := familyFile(t, s, "tools/fixture/core.mjs"); got != "export const core = 1;\n" {
		t.Fatalf("--force did not regenerate drift: %q", got)
	}
	permissive := familySession(t, true)
	family.RefuseForeign = false
	mustWrite(t, filepath.Join(permissive.repoPath, "tools", "fixture", "core.mjs"), "// operator\n")
	if err := reconcileManagedFamily(t.Context(), permissive, family); err != nil {
		t.Fatalf("a family without refuse-on-first-adopt refused: %v", err)
	}
}

// Negative: disabling refuses a drift in any family before it removes anything of any family.
func TestManagedFamilyRemovalRefusesBeforeDeleting(t *testing.T) {
	s := familySession(t, false)
	markdown := DocumentationFamilies()
	families := append(slices.Clone(markdown), secondFamily())
	for _, family := range families {
		if err := reconcileManagedFamily(t.Context(), s, family); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite(t, filepath.Join(s.repoPath, "tools", "fixture", "core.mjs"), "// operator edit\n")
	err := removeManagedFamilies(t.Context(), s, families)
	if err == nil || err.Error() != "refusing to remove drifted documentation asset tools/fixture/core.mjs" {
		t.Fatalf("removal over drift: %v", err)
	}
	if _, ok := familyFile(t, s, DocumentationWorkflowFile); !ok {
		t.Fatal("the Markdown family lost a file before the refusal")
	}
}

const (
	// priorFixtureWorkflow and priorFixtureCore are earlier texts of secondFamily's workflow
	// and core.mjs.
	priorFixtureWorkflow = "name: Fixture Gate\non: push\n"
	priorFixtureCore     = "export const core = 0;\n"
	refreshedPriorDetail = "Refreshed an earlier Praetor text to the current locked text"
)

// priorFamily is secondFamily recognising an earlier text of its workflow and of core.mjs.
func priorFamily() managedasset.Family {
	family := secondFamily()
	family.Prior = map[string]string{}
	for text, rel := range map[string]string{priorFixtureWorkflow: family.WorkflowFile, priorFixtureCore: "tools/fixture/core.mjs"} {
		sum := sha256.Sum256([]byte(text))
		family.Prior[hex.EncodeToString(sum[:])] = rel
	}
	return family
}

// Positive: plain adoption refreshes exact earlier Praetor texts, keeping a file's CRLF
// style, and a family whose only files are earlier texts counts as adopted, so
// refuse-on-first-adopt does not fire.
func TestManagedFamilyRefreshesPriorTextWithoutForce(t *testing.T) {
	s := familySession(t, false)
	family := priorFamily()
	mustWrite(t, filepath.Join(s.repoPath, ".github", "workflows", "fixture.yml"), priorFixtureWorkflow)
	mustWrite(t, filepath.Join(s.repoPath, "tools", "fixture", "core.mjs"), strings.ReplaceAll(priorFixtureCore, "\n", "\r\n"))
	if err := reconcileManagedFamily(t.Context(), s, family); err != nil {
		t.Fatal(err)
	}
	for rel, want := range map[string]string{
		".github/workflows/fixture.yml":         family.Workflow,
		"tools/fixture/core.mjs":                "export const core = 1;\r\n",
		"tools/fixture/third_party/lib/LICENSE": "MIT\n",
	} {
		if got, _ := familyFile(t, s, rel); got != want {
			t.Fatalf("%s = %q, want %q", rel, got, want)
		}
	}
	for _, rel := range []string{".github/workflows/fixture.yml", "tools/fixture/core.mjs"} {
		if action, _ := actionOf(s.report, rel); action.Action != actionReconcile || action.Details != refreshedPriorDetail {
			t.Fatalf("%s action = %+v", rel, action)
		}
	}
}

// Negative: an edited earlier text is not Praetor's output and keeps the --force contract, and
// a dry run reports a refresh without writing it.
func TestManagedFamilyPriorTextKeepsForceContract(t *testing.T) {
	s := familySession(t, false)
	family := priorFamily()
	edited := priorFixtureCore + "// edited\n"
	mustWrite(t, filepath.Join(s.repoPath, ".github", "workflows", "fixture.yml"), family.Workflow)
	mustWrite(t, filepath.Join(s.repoPath, "tools", "fixture", "core.mjs"), edited)
	if err := reconcileManagedFamily(t.Context(), s, family); err != nil {
		t.Fatal(err)
	}
	if got, _ := familyFile(t, s, "tools/fixture/core.mjs"); got != edited {
		t.Fatalf("an edited earlier text was overwritten without --force: %q", got)
	}
	dry := familySession(t, false)
	dry.opts.DryRun = true
	mustWrite(t, filepath.Join(dry.repoPath, ".github", "workflows", "fixture.yml"), priorFixtureWorkflow)
	if err := reconcileManagedFamily(t.Context(), dry, family); err != nil {
		t.Fatal(err)
	}
	if got, _ := familyFile(t, dry, ".github/workflows/fixture.yml"); got != priorFixtureWorkflow {
		t.Fatalf("a dry run refreshed an earlier text: %q", got)
	}
	if action, _ := actionOf(dry.report, ".github/workflows/fixture.yml"); action.Details != refreshedPriorDetail {
		t.Fatalf("dry-run action = %+v", action)
	}
}

// Boundary: disabling removes an earlier text like the canonical one, while an earlier text
// at a path its digest does not name is drift and stops the removal.
func TestManagedFamilyRemovesPriorText(t *testing.T) {
	s := familySession(t, false)
	family := priorFamily()
	if err := reconcileManagedFamily(t.Context(), s, family); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(s.repoPath, ".github", "workflows", "fixture.yml"), priorFixtureWorkflow)
	mustWrite(t, filepath.Join(s.repoPath, "tools", "fixture", "core.mjs"), priorFixtureWorkflow)
	err := removeManagedFamilies(t.Context(), s, []managedasset.Family{family})
	if err == nil || err.Error() != "refusing to remove drifted documentation asset tools/fixture/core.mjs" {
		t.Fatalf("removal over a misplaced earlier text: %v", err)
	}
	mustWrite(t, filepath.Join(s.repoPath, "tools", "fixture", "core.mjs"), priorFixtureCore)
	if err := removeManagedFamilies(t.Context(), s, []managedasset.Family{family}); err != nil {
		t.Fatal(err)
	}
	for _, rel := range family.ManagedPaths() {
		if _, ok := familyFile(t, s, rel); ok {
			t.Fatalf("%s survived removal", rel)
		}
	}
}

// Positive and boundary: the documentation facet selects the registry's families, and every
// documentation asset path belongs to exactly one of them.
func TestDocumentationFamiliesCoverAssetPaths(t *testing.T) {
	families := DocumentationFamilies()
	if len(families) == 0 {
		t.Fatal("docs:seo-portal enables no family")
	}
	for _, rel := range DocumentationAssetPaths() {
		owner, owned, err := managedFamilyOwning(families, rel)
		if err != nil || !owned || owner.Facet != managedasset.DocumentationFacet {
			t.Fatalf("%s: owner=%s owned=%v err=%v", rel, owner.Name, owned, err)
		}
	}
	if _, err := DocumentationAssetIsCanonical("tools/other/file.txt", nil); err == nil {
		t.Fatal("a path no documentation family owns was classified")
	}
}

// gitAttributesShape is a .gitattributes-style block through the same tail-block merger the
// ignore files use.
func gitAttributesShape() managedTailBlock {
	return managedTailBlock{
		file: ".gitattributes", begin: "# BEGIN fixture attributes", end: "# END fixture attributes",
		maxLines: 8, duplicate: "duplicate managed markers",
	}
}

// Positive: the tail-block merger renders and places a block for another file: operator lines
// kept, CRLF preserved, an old block replaced at the tail.
func TestManagedTailBlockReuse(t *testing.T) {
	shape := gitAttributesShape()
	block := shape.render([]string{"tools/fixture/** text eol=lf"})
	if block != "# BEGIN fixture attributes\ntools/fixture/** text eol=lf\n# END fixture attributes\n" {
		t.Fatalf("render = %q", block)
	}
	merged, err := shape.merge("* text=auto\r\n", block)
	if err != nil || merged != "* text=auto\r\n\r\n"+strings.ReplaceAll(block, "\n", "\r\n") {
		t.Fatalf("CRLF merge = %q, %v", merged, err)
	}
	again, err := shape.merge("a\n"+block+"b\n", block)
	if err != nil || again != "a\nb\n\n"+block {
		t.Fatalf("block not moved to the tail: %q, %v", again, err)
	}
}

// Negative: ambiguous markers are refused with the file's own name and duplicate wording.
func TestManagedTailBlockRefusesAmbiguousMarkers(t *testing.T) {
	shape := gitAttributesShape()
	block := shape.render([]string{"x"})
	for text, want := range map[string]string{
		block + block:                   ".gitattributes contains duplicate managed markers",
		"# BEGIN fixture attributes\n":  ".gitattributes contains an unterminated managed block",
		"# END fixture attributes\n":    ".gitattributes contains an unmatched managed end marker",
		" # BEGIN fixture attributes\n": ".gitattributes contains a non-canonical managed marker at line 1",
		"a\r\nb\n":                      ".gitattributes line endings are inconsistent",
	} {
		if _, err := shape.merge(text, block); err == nil || !strings.HasPrefix(err.Error(), want) {
			t.Fatalf("merge(%q) = %v, want %q", text, err, want)
		}
	}
}

// Boundary: a file of exactly maxLines lines merges, one more line is refused, and the render
// bound cuts an oversized line list.
func TestManagedTailBlockBoundary(t *testing.T) {
	shape := gitAttributesShape()
	block := shape.render([]string{"x"})
	if _, err := shape.merge(strings.Repeat("a\n", 7), block); err != nil {
		t.Fatalf("a file at the line bound was refused: %v", err)
	}
	if _, err := shape.merge(strings.Repeat("a\n", 8), block); err == nil {
		t.Fatal("a file above the line bound merged")
	}
	if got := strings.Count(shape.render(slices.Repeat([]string{"x"}, 20)), "\n"); got != 10 {
		t.Fatalf("render past the bound wrote %d lines, want 8 plus both markers", got)
	}
}

// ManagedFileIsCanonical: positive for the canonical text in either consistent line-ending
// style, negative for different text and for a path the family does not own, boundary for
// mixed endings, which are never canonical and never an error.
func TestManagedFileIsCanonical(t *testing.T) {
	family := secondFamily()
	for _, text := range []string{"MIT\n", "MIT\r\n"} {
		if canonical, err := ManagedFileIsCanonical(family, "tools/fixture/third_party/lib/LICENSE", []byte(text)); err != nil || !canonical {
			t.Fatalf("%q: canonical=%v err=%v", text, canonical, err)
		}
	}
	if canonical, err := ManagedFileIsCanonical(family, "tools/fixture/core.mjs", []byte("// other\n")); err != nil || canonical {
		t.Fatalf("different text: canonical=%v err=%v", canonical, err)
	}
	if _, err := ManagedFileIsCanonical(family, "tools/fixture/unknown.mjs", nil); err == nil {
		t.Fatal("a path the family does not own was classified")
	}
	mixed := []byte("name: Fixture Gate\r\n\n")
	if canonical, err := ManagedFileIsCanonical(family, ".github/workflows/fixture.yml", mixed); err != nil || canonical {
		t.Fatalf("mixed endings: canonical=%v err=%v", canonical, err)
	}
}

// ManagedFileIsPraetors: positive for the canonical text and for an earlier text in either
// consistent line-ending style, negative for an edited earlier text, for an earlier text at a
// path its digest does not name and for a path the family does not own, boundary for an
// earlier text with mixed endings, which is neither and never an error.
func TestManagedFileIsPraetors(t *testing.T) {
	family := priorFamily()
	for rel, text := range map[string]string{
		"tools/fixture/third_party/lib/LICENSE": "MIT\n",
		".github/workflows/fixture.yml":         priorFixtureWorkflow,
		"tools/fixture/core.mjs":                strings.ReplaceAll(priorFixtureCore, "\n", "\r\n"),
	} {
		if praetors, err := ManagedFileIsPraetors(family, rel, []byte(text)); err != nil || !praetors {
			t.Fatalf("%s holding %q: praetors=%v err=%v", rel, text, praetors, err)
		}
	}
	for rel, text := range map[string]string{
		".github/workflows/fixture.yml": priorFixtureWorkflow + "# edited\n",
		"tools/fixture/core.mjs":        priorFixtureWorkflow,
	} {
		if praetors, err := ManagedFileIsPraetors(family, rel, []byte(text)); err != nil || praetors {
			t.Fatalf("%s holding %q: praetors=%v err=%v", rel, text, praetors, err)
		}
	}
	if _, err := ManagedFileIsPraetors(family, "tools/fixture/unknown.mjs", nil); err == nil {
		t.Fatal("a path the family does not own was classified")
	}
	mixed := []byte(strings.Replace(priorFixtureWorkflow, "\n", "\r\n", 1))
	if praetors, err := ManagedFileIsPraetors(family, ".github/workflows/fixture.yml", mixed); err != nil || praetors {
		t.Fatalf("mixed endings: praetors=%v err=%v", praetors, err)
	}
}
