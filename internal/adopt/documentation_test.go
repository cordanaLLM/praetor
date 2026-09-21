package adopt

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/util"
	markdownassets "github.com/cordanaLLM/praetor/tools/markdownlint"
	"gopkg.in/yaml.v3"
)

func TestAdoptionDocumentationGatePositive(t *testing.T) {
	root := newTestRepo(t, "documentation-positive")
	mustWrite(t, filepath.Join(root, readmeFile), "# Documentation fixture\n")
	opts := AdoptOptions{Path: root, Profile: "framework", LockSourceRoot: newAdoptLockSource(t)}
	if _, err := Adopt(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
	assertDocumentationAssets(t, root)
	makefile := mustRead(t, filepath.Join(root, makefileName))
	if !strings.Contains(makefile, DocumentationMakefileBlock()) {
		t.Fatal("verify-all lacks the managed documentation gate")
	}
	ignore := mustRead(t, filepath.Join(root, gitIgnoreFile))
	for _, rule := range []string{"/.workingdir/", "/.workingdir2/"} {
		if countIgnoreRule(ignore, rule) != 1 {
			t.Fatalf("privacy rule %q is absent or duplicated", rule)
		}
	}
	contexts, err := forge.RequiredStatusContexts(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(contexts, DocumentationStatusContext) {
		t.Fatalf("required contexts %v lack %q", contexts, DocumentationStatusContext)
	}
	manifest, err := config.LoadManifest(filepath.Join(root, manifestFile))
	if err != nil {
		t.Fatal(err)
	}
	workflowURL := "https://github.com/" + manifest.Repository.Owner + "/" + manifest.Repository.Name +
		"/actions/workflows/praetor-docs.yml"
	readme := mustRead(t, filepath.Join(root, readmeFile))
	for _, want := range []string{
		"[![Documentation Governance](" + workflowURL + "/badge.svg)](" + workflowURL + ")",
		"| **Documentation** | `make docs-lint` | Enforces locked Markdown style and private scratch-link policy |",
	} {
		if strings.Count(readme, want) != 1 {
			t.Fatalf("adopted README must contain one exact documentation contract %q:\n%s", want, readme)
		}
	}
	before := snapshotTree(t, root)
	if _, err := Adopt(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
	assertTreeUnchanged(t, before, snapshotTree(t, root))
}

func TestAdoptionDocumentationGateNegativeFacet(t *testing.T) {
	root := newTestRepo(t, "documentation-disabled")
	opts := AdoptOptions{
		Path: root, Profile: "framework", Facets: []string{"custom:facet"},
		LockSourceRoot: newAdoptLockSource(t),
	}
	if _, err := Adopt(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{DocumentationWorkflowFile, filepath.Join(markdownassets.Directory, "package.json")} {
		if _, err := os.Lstat(filepath.Join(root, rel)); !os.IsNotExist(err) {
			t.Fatalf("documentation-disabled adoption emitted %s", rel)
		}
	}
	if strings.Contains(mustRead(t, filepath.Join(root, makefileName)), documentationMakefileBegin) {
		t.Fatal("documentation-disabled adoption attached docs-lint")
	}
}

func TestAdoptionDocumentationGateForceRefresh(t *testing.T) {
	root := newTestRepo(t, "documentation-refresh")
	opts := AdoptOptions{Path: root, Profile: "framework", LockSourceRoot: newAdoptLockSource(t)}
	if _, err := Adopt(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
	asset := filepath.Join(root, markdownassets.Directory, "package.json")
	mustWrite(t, asset, "{}\n")
	if _, err := Adopt(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, asset); got != "{}\n" {
		t.Fatal("ordinary adoption overwrote an existing managed asset")
	}
	opts.Force = true
	if _, err := Adopt(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
	want, err := markdownassets.Read("package.json")
	if err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, asset); got != string(want) {
		t.Fatal("force did not restore the exact locked asset")
	}
}

func TestAdoptionDocumentationGateFacetTransitionConverges(t *testing.T) {
	root := newTestRepo(t, "documentation-transition")
	mustWrite(t, filepath.Join(root, readmeFile), "# Transition\n")
	mustWrite(t, filepath.Join(root, makefileName), "all:\n\t@echo operator\n")
	mustWrite(t, filepath.Join(root, ".prettierrc"), "{}\n")
	mustWrite(t, filepath.Join(root, prettierIgnoreFile), "operator-output/\n")
	opts := AdoptOptions{Path: root, Profile: "framework", LockSourceRoot: newAdoptLockSource(t)}
	if _, err := Adopt(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(root, markdownassets.Directory, "operator.txt"), "preserve\n")
	setDocumentationFacet(t, root, false)
	opts.Force = true
	if _, err := Adopt(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
	assertDocumentationDisabled(t, root)
	if got := mustRead(t, filepath.Join(root, markdownassets.Directory, "operator.txt")); got != "preserve\n" {
		t.Fatalf("operator file inside documentation tool directory changed: %q", got)
	}
	disabled := snapshotTree(t, root)
	opts.Force = false
	if _, err := Adopt(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
	assertTreeUnchanged(t, disabled, snapshotTree(t, root))

	setDocumentationFacet(t, root, true)
	opts.Force = true
	if _, err := Adopt(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
	assertDocumentationAssets(t, root)
	readme := mustRead(t, filepath.Join(root, readmeFile))
	if !strings.Contains(readme, "[![Documentation Governance]") || !strings.Contains(readme, "`make docs-lint`") {
		t.Fatalf("re-enabled README lacks documentation contract:\n%s", readme)
	}
	if !strings.Contains(mustRead(t, filepath.Join(root, prettierIgnoreFile)), DocumentationWorkflowFile) {
		t.Fatal("re-enabled formatter inventory lacks documentation workflow")
	}
	enabled := snapshotTree(t, root)
	opts.Force = false
	if _, err := Adopt(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
	assertTreeUnchanged(t, enabled, snapshotTree(t, root))
}

func TestAdoptionDocumentationGateCRLFConverges(t *testing.T) {
	root := newTestRepo(t, "documentation-crlf-transition")
	operatorMakefile := ".PHONY: verify-all\r\nverify-all:\r\n\t@echo operator\r\n"
	mustWrite(t, filepath.Join(root, makefileName), operatorMakefile)
	opts := AdoptOptions{Path: root, Profile: "framework", LockSourceRoot: newAdoptLockSource(t)}
	if _, err := Adopt(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
	makefilePath := filepath.Join(root, makefileName)
	assertConsistentCRLF(t, mustRead(t, makefilePath))
	for _, rel := range DocumentationAssetPaths() {
		path := filepath.Join(root, filepath.FromSlash(rel))
		mustWrite(t, path, crlfText(mustRead(t, path)))
	}
	enabled := snapshotTree(t, root)
	opts.Force = true
	if _, err := Adopt(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
	assertTreeUnchanged(t, enabled, snapshotTree(t, root))
	setDocumentationFacet(t, root, false)
	if _, err := Adopt(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, makefilePath); got != operatorMakefile {
		t.Fatalf("CRLF disable did not restore operator Makefile: %q", got)
	}
	for _, rel := range DocumentationAssetPaths() {
		if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel))); !os.IsNotExist(err) {
			t.Fatalf("CRLF documentation asset remains at %s: %v", rel, err)
		}
	}
}

func TestAdoptionDocumentationDisableRejectsDrift(t *testing.T) {
	root := newTestRepo(t, "documentation-disable-drift")
	opts := AdoptOptions{Path: root, Profile: "framework", LockSourceRoot: newAdoptLockSource(t)}
	if _, err := Adopt(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
	asset := filepath.Join(root, markdownassets.Directory, "package.json")
	mustWrite(t, asset, "operator edit\n")
	setDocumentationFacet(t, root, false)
	opts.Force = true
	if _, err := Adopt(t.Context(), opts); err == nil {
		t.Fatal("documentation disable deleted a drifted managed asset")
	}
	if got := mustRead(t, asset); got != "operator edit\n" {
		t.Fatalf("drifted asset changed after rejected disable: %q", got)
	}
}

func TestAdoptionDocumentationDisableRejectsMixedAssetEndings(t *testing.T) {
	root := newTestRepo(t, "documentation-disable-mixed-eol")
	opts := AdoptOptions{Path: root, Profile: "framework", LockSourceRoot: newAdoptLockSource(t)}
	if _, err := Adopt(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
	asset := filepath.Join(root, markdownassets.Directory, "package.json")
	mixed := strings.Replace(mustRead(t, asset), "\n", "\r\n", 1)
	mustWrite(t, asset, mixed)
	setDocumentationFacet(t, root, false)
	opts.Force = true
	if _, err := Adopt(t.Context(), opts); err == nil {
		t.Fatal("documentation disable accepted mixed asset line endings")
	}
	if got := mustRead(t, asset); got != mixed {
		t.Fatalf("mixed asset changed after rejected disable: %q", got)
	}
	if _, err := os.Lstat(filepath.Join(root, DocumentationWorkflowFile)); err != nil {
		t.Fatalf("disable removed another asset before rejecting mixed endings: %v", err)
	}
}

func TestAdoptionDocumentationDisableRequiresForceBeforeHostedContextRewrite(t *testing.T) {
	root := newTestRepo(t, "documentation-disable-requires-force")
	opts := AdoptOptions{Path: root, Profile: "framework", LockSourceRoot: newAdoptLockSource(t)}
	if _, err := Adopt(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
	setDocumentationFacet(t, root, false)
	if _, err := Adopt(t.Context(), opts); err == nil {
		t.Fatal("documentation disable preserved a stale hosted context after deleting local assets")
	}
	if _, err := os.Lstat(filepath.Join(root, DocumentationWorkflowFile)); err != nil {
		t.Fatalf("non-forced disable removed assets before refusing the hosted-context rewrite: %v", err)
	}
}

func TestAdoptionDocumentationDisableRejectsSymlink(t *testing.T) {
	root := newTestRepo(t, "documentation-disable-symlink")
	opts := AdoptOptions{Path: root, Profile: "framework", LockSourceRoot: newAdoptLockSource(t)}
	if _, err := Adopt(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "outside-package.json")
	mustWrite(t, target, "preserve\n")
	asset := filepath.Join(root, markdownassets.Directory, "package.json")
	if err := os.Remove(asset); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, asset); err != nil {
		t.Fatal(err)
	}
	setDocumentationFacet(t, root, false)
	opts.Force = true
	if _, err := Adopt(t.Context(), opts); err == nil {
		t.Fatal("documentation disable accepted a symlinked managed asset")
	}
	if got := mustRead(t, target); got != "preserve\n" {
		t.Fatalf("symlink target changed after rejected disable: %q", got)
	}
	if _, err := os.Lstat(filepath.Join(root, DocumentationWorkflowFile)); err != nil {
		t.Fatalf("disable removed another asset before rejecting the symlink: %v", err)
	}
}

func TestAdoptionDocumentationDisableRejectsEditedMakefileBeforeDeletingAssets(t *testing.T) {
	root := newTestRepo(t, "documentation-disable-makefile-drift")
	opts := AdoptOptions{Path: root, Profile: "framework", LockSourceRoot: newAdoptLockSource(t)}
	if _, err := Adopt(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
	makefilePath := filepath.Join(root, makefileName)
	edited := strings.Replace(mustRead(t, makefilePath), "@node tools/markdownlint/verify.mjs",
		"@node tools/markdownlint/verify.mjs --operator-edit", 1)
	mustWrite(t, makefilePath, edited)
	setDocumentationFacet(t, root, false)
	opts.Force = true
	if _, err := Adopt(t.Context(), opts); err == nil {
		t.Fatal("documentation disable accepted an edited managed Makefile block")
	}
	if got := mustRead(t, makefilePath); got != edited {
		t.Fatal("rejected documentation disable changed the edited Makefile")
	}
	if _, err := os.Lstat(filepath.Join(root, DocumentationWorkflowFile)); err != nil {
		t.Fatalf("disable removed assets before rejecting the edited Makefile: %v", err)
	}
}

func TestAdoptionDocumentationDisableRejectsAmbiguousFormatterBeforeDeletingAssets(t *testing.T) {
	root := newTestRepo(t, "documentation-disable-formatter-ambiguity")
	mustWrite(t, filepath.Join(root, ".prettierrc"), "{}\n")
	mustWrite(t, filepath.Join(root, prettierIgnoreFile), "operator-before/\n")
	opts := AdoptOptions{Path: root, Profile: "framework", LockSourceRoot: newAdoptLockSource(t)}
	if _, err := Adopt(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
	formatterPath := filepath.Join(root, prettierIgnoreFile)
	ambiguous := strings.Replace(mustRead(t, formatterPath), managedIgnoreEnd+"\n",
		"operator-after/\n", 1)
	mustWrite(t, formatterPath, ambiguous)
	setDocumentationFacet(t, root, false)
	opts.Force = true
	if _, err := Adopt(t.Context(), opts); err == nil {
		t.Fatal("documentation disable accepted an unterminated formatter block")
	}
	if got := mustRead(t, formatterPath); got != ambiguous {
		t.Fatal("rejected documentation disable changed formatter operator content")
	}
	if _, err := os.Lstat(filepath.Join(root, DocumentationWorkflowFile)); err != nil {
		t.Fatalf("disable removed assets before rejecting formatter ambiguity: %v", err)
	}
}

func setDocumentationFacet(t *testing.T, root string, enabled bool) {
	t.Helper()
	path := filepath.Join(root, manifestFile)
	manifest, err := config.LoadManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Facets = slices.DeleteFunc(manifest.Facets,
		func(facet string) bool { return facet == "docs:seo-portal" })
	if enabled {
		manifest.Facets = append(manifest.Facets, "docs:seo-portal")
	}
	data, err := yaml.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, path, string(data))
}

func assertDocumentationDisabled(t *testing.T, root string) {
	t.Helper()
	for _, rel := range DocumentationAssetPaths() {
		if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel))); !os.IsNotExist(err) {
			t.Fatalf("disabled documentation artifact remains at %s: %v", rel, err)
		}
	}
	makefile := mustRead(t, filepath.Join(root, makefileName))
	if !strings.Contains(makefile, "@echo operator") || strings.Contains(makefile, documentationMakefileBegin) {
		t.Fatalf("documentation disable damaged operator Makefile or retained managed block:\n%s", makefile)
	}
	readme := mustRead(t, filepath.Join(root, readmeFile))
	if strings.Contains(readme, "Documentation Governance") || strings.Contains(readme, "`make docs-lint`") {
		t.Fatalf("disabled README retained documentation contract:\n%s", readme)
	}
	if strings.Contains(mustRead(t, filepath.Join(root, rulesetFile)), DocumentationStatusContext) {
		t.Fatal("disabled ruleset retained documentation status context")
	}
	ignore := mustRead(t, filepath.Join(root, prettierIgnoreFile))
	if !strings.Contains(ignore, "operator-output/") || strings.Contains(ignore, DocumentationWorkflowFile) ||
		strings.Contains(ignore, markdownassets.Directory+"/") {
		t.Fatalf("disabled formatter inventory did not converge:\n%s", ignore)
	}
}

func TestMergeDocumentationMakefileBoundary(t *testing.T) {
	custom := "all:\n\t@echo custom\n"
	merged, err := mergeDocumentationMakefile(custom, false)
	if err != nil || !strings.HasPrefix(merged, custom) || strings.Count(merged, documentationMakefileBegin) != 1 {
		t.Fatalf("custom Makefile was not preserved: err=%v\n%s", err, merged)
	}
	if _, err := mergeDocumentationMakefile("docs-lint:\n\t@echo operator\n", false); err == nil {
		t.Fatal("operator-owned docs-lint collision accepted")
	}
	if _, err := mergeDocumentationMakefile(DocumentationMakefileBlock()+DocumentationMakefileBlock(), true); err == nil {
		t.Fatal("duplicate managed blocks accepted")
	}
	edited := strings.Replace(DocumentationMakefileBlock(), "@node", "@npx", 1)
	if _, err := mergeDocumentationMakefile(edited, false); err == nil {
		t.Fatal("edited managed block accepted without force")
	}
	repaired, err := mergeDocumentationMakefile(edited, true)
	if err != nil || repaired != DocumentationMakefileBlock() {
		t.Fatalf("force did not restore the exact block: err=%v\n%s", err, repaired)
	}
}

func TestDocumentationSurfaceClassifiers(t *testing.T) {
	asset, err := markdownassets.Read("package.json")
	if err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string][]byte{
		"LF":   asset,
		"CRLF": []byte(strings.ReplaceAll(string(asset), "\n", "\r\n")),
	} {
		t.Run(name, func(t *testing.T) {
			canonical, classifyErr := DocumentationAssetIsCanonical(
				markdownassets.Directory+"/package.json", content,
			)
			if classifyErr != nil || !canonical {
				t.Fatalf("canonical asset missed: canonical=%v err=%v", canonical, classifyErr)
			}
		})
	}
	canonical, err := DocumentationAssetIsCanonical(
		markdownassets.Directory+"/package.json", []byte("{\"operator\":true}\n"),
	)
	if err != nil || canonical {
		t.Fatalf("operator asset claimed: canonical=%v err=%v", canonical, err)
	}
	markers, err := DocumentationMakefileMarkersPresent(
		"# prose mentions " + documentationMakefileBegin + " inline\noperator:\n\t@true\n",
	)
	if err != nil || markers {
		t.Fatalf("operator prose claimed as marker: markers=%v err=%v", markers, err)
	}
	markers, err = DocumentationMakefileMarkersPresent(DocumentationMakefileBlock())
	if err != nil || !markers {
		t.Fatalf("canonical marker missed: markers=%v err=%v", markers, err)
	}
	exactBoundary := strings.Repeat("operator\n", maxMakefileLines-1) + documentationMakefileBegin
	markers, err = DocumentationMakefileMarkersPresent(exactBoundary)
	if err != nil || !markers {
		t.Fatalf("marker at exact line boundary missed: markers=%v err=%v", markers, err)
	}
	if _, err := DocumentationMakefileMarkersPresent(exactBoundary + "\noverflow"); err == nil {
		t.Fatal("Makefile above marker scan bound accepted")
	}
}

func TestDocumentationEnabledUsesValidatedFacetBound(t *testing.T) {
	facets := make([]string, config.MaxManifestEntriesPerKind)
	for index := 0; index < len(facets) && index < config.MaxManifestEntriesPerKind; index++ {
		facets[index] = "custom"
	}
	facets[len(facets)-1] = "docs:seo-portal"
	enabled, err := DocumentationEnabled(facets)
	if err != nil || !enabled {
		t.Fatalf("documentation facet at boundary missed: enabled=%v err=%v", enabled, err)
	}
	if _, err := DocumentationEnabled(append(facets, "overflow")); err == nil {
		t.Fatal("facet inventory above validated bound accepted")
	}
}

func TestMergeDocumentationMakefileRejectsAmbiguousOwnership(t *testing.T) {
	for name, makefile := range map[string]string{
		"include":      "include docs.mk\n",
		"soft include": "-include generated.mk\n",
		"dynamic":      "target := docs-lint\n$(target):\n\t@true\n",
		"eval":         "$(eval docs-lint: ; @true)\n",
		"pattern":      "docs-%:\n\t@true\n",
		"catch all":    "%:\n\t@true\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := mergeDocumentationMakefile(makefile, false); err == nil {
				t.Fatal("ambiguous docs-lint ownership accepted")
			}
		})
	}
}

func TestMergeDocumentationMakefileGNUReplay(t *testing.T) {
	custom := ".PHONY: operator\noperator:\n\t@echo operator\n"
	merged, err := mergeDocumentationMakefile(custom, false)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, makefileName), merged)
	out, err := util.RunCommand(t.Context(), root, "make", "--no-print-directory", "-n", "docs-lint")
	if err != nil {
		t.Fatalf("GNU Make rejected unambiguous merged file: %s %v", out, err)
	}
	if strings.Count(out, "node tools/markdownlint/verify.mjs") != 1 {
		t.Fatalf("GNU Make replay did not select exactly one managed recipe: %q", out)
	}
}

func TestMergeDocumentationMakefileCRLF(t *testing.T) {
	existing := ".PHONY: verify-all\r\nverify-all:\r\n\t@echo operator\r\n"
	merged, err := mergeDocumentationMakefile(existing, false)
	if err != nil {
		t.Fatal(err)
	}
	assertConsistentCRLF(t, merged)
	if !strings.Contains(merged, crlfText(DocumentationMakefileBlock())) {
		t.Fatalf("CRLF Makefile lacks canonical documentation block: %q", merged)
	}
	second, err := mergeDocumentationMakefile(merged, false)
	if err != nil || second != merged {
		t.Fatalf("CRLF Makefile did not converge: err=%v\n%q", err, second)
	}
	removed, ok, err := removeDocumentationMakefileBlock(merged)
	if err != nil || !ok || removed != existing {
		t.Fatalf("CRLF Makefile removal: removed=%v err=%v\n%q", ok, err, removed)
	}
	mixed := strings.Replace(merged, "\r\n", "\n", 1)
	if _, err := mergeDocumentationMakefile(mixed, true); err == nil {
		t.Fatal("mixed-ending Makefile accepted for merge")
	}
	if _, _, err := removeDocumentationMakefileBlock(mixed); err == nil {
		t.Fatal("mixed-ending Makefile accepted for removal")
	}
	appended, err := appendVerificationTargets("all:\r\n\t@echo operator\r\n", &VerificationPlan{})
	if err != nil {
		t.Fatal(err)
	}
	assertConsistentCRLF(t, appended)
}

func crlfText(value string) string {
	return strings.ReplaceAll(value, "\n", "\r\n")
}

func assertConsistentCRLF(t *testing.T, value string) {
	t.Helper()
	if !strings.Contains(value, "\r\n") || strings.Contains(strings.ReplaceAll(value, "\r\n", ""), "\n") {
		t.Fatalf("text is not consistently CRLF: %q", value)
	}
}

func TestAdoptionDocumentationAssetRejectsSymlink(t *testing.T) {
	root := newTestRepo(t, "documentation-symlink")
	target := filepath.Join(t.TempDir(), "outside-package.json")
	mustWrite(t, target, "preserve")
	asset := filepath.Join(root, markdownassets.Directory, "package.json")
	if err := os.MkdirAll(filepath.Dir(asset), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, asset); err != nil {
		t.Fatal(err)
	}
	report, err := Adopt(t.Context(), AdoptOptions{
		Path: root, Profile: "framework", LockSourceRoot: newAdoptLockSource(t), Force: true,
	})
	if err == nil && len(report.Errors) == 0 {
		t.Fatal("linked documentation asset accepted")
	}
	if got := mustRead(t, target); got != "preserve" {
		t.Fatalf("symlink target changed: %q", got)
	}
}

func assertDocumentationAssets(t *testing.T, root string) {
	t.Helper()
	for _, name := range markdownassets.Names() {
		want, err := markdownassets.Read(name)
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(filepath.Join(root, markdownassets.Directory, name))
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("asset %s differs: err=%v", name, err)
		}
	}
	workflow, err := os.ReadFile(filepath.Join(root, DocumentationWorkflowFile))
	if err != nil || string(workflow) != DocumentationWorkflow() {
		t.Fatalf("workflow differs: err=%v", err)
	}
}
