// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/classify"
	"github.com/cordanaLLM/praetor/internal/testsupport"
)

// The figure engine joined docs:seo-portal as the second managed asset family
// (docs/adr/0016-figures-for-adopters.md, section 5). These tests pin what adoption writes,
// refuses and removes for it through the registry, the .gitattributes block its hashed files
// need, and the docs-figures target of the managed Makefile block.

// figureFamilyGoldenPaths are the figure paths a golden records, spelled out rather than read
// from the code under test: the wrapper, the player, the vendored license, the guide, and the
// attribute file.
var figureFamilyGoldenPaths = []string{
	"tools/figures/build.mjs",
	"tools/figures/dist/player.js",
	"tools/figures/third_party/interfig/upstream/LICENSE",
	"tools/figures/README.md",
	".gitattributes",
}

// figureAttributeBlock is the block adoption writes for the figure engine, spelled out.
const figureAttributeBlock = "# BEGIN praetor managed attributes (praetorctl adopt)\n" +
	"# praetorctl audit hashes these files; keep their checkout bytes the same on every platform.\n" +
	"tools/figures/** text eol=lf\n" +
	"docs/figures/*.ts text eol=lf\n" +
	"docs/assets/figures/* text eol=lf\n" +
	"tools/figures/third_party/interfig/upstream/** -text\n" +
	"# END praetor managed attributes\n"

// Positive: enable writes every figure asset and the attribute block after the operator's rules,
// a rerun changes nothing, a drifted asset is kept without --force and regenerated with it,
// disable removes the assets and the block but keeps the operator's rules, and re-enable
// restores both.
func TestFigureFamilyAdoptionGolden(t *testing.T) {
	golden := &familyGolden{t: t, paths: figureFamilyGoldenPaths}
	root := newTestRepo(t, "figure-golden-lifecycle")
	mustWrite(t, filepath.Join(root, gitAttributesFile), "* text=auto\n")
	opts := goldenAdoptOptions(t, root)
	golden.adopt("fresh enable", root, opts)
	golden.adopt("rerun", root, opts)
	mustWrite(t, filepath.Join(root, "tools", "figures", "build.mjs"), "// operator edit\n")
	golden.adopt("drift without force", root, opts)
	opts.Force = true
	golden.adopt("drift with force", root, opts)
	setDocumentationFacet(t, root, false)
	golden.adopt("disable with force", root, opts)
	golden.adopt("disabled rerun", root, opts)
	setDocumentationFacet(t, root, true)
	golden.adopt("re-enable with force", root, opts)
	testsupport.AssertGolden(t, filepath.Join("testdata", "managed-family", "figure-lifecycle.golden"), golden.sb.String())
}

// Negative and boundary: a file the repository already had at a managed figure path stops first
// adoption with and without --force, before any figure asset or the attribute block is written;
// a file under tools/figures that is not a managed path does not; and a disable over a drifted
// figure asset is refused before anything is removed.
func TestFigureFamilyForeignFilesGolden(t *testing.T) {
	golden := &familyGolden{t: t, paths: figureFamilyGoldenPaths}
	for _, force := range []bool{false, true} {
		root := newTestRepo(t, fmt.Sprintf("figure-golden-foreign-%t", force))
		mustWrite(t, filepath.Join(root, "tools", "figures", "build.mjs"), "// the repository's own build script\n")
		mustWrite(t, filepath.Join(root, gitAttributesFile), "* text=auto\n")
		opts := goldenAdoptOptions(t, root)
		opts.Force = force
		golden.adopt(fmt.Sprintf("foreign first adoption force=%t", force), root, opts)
	}
	root := newTestRepo(t, "figure-golden-unmanaged-neighbour")
	mustWrite(t, filepath.Join(root, "tools", "figures", "notes.md"), "# Notes\n")
	golden.adopt("unmanaged file under tools/figures", root, goldenAdoptOptions(t, root))
	mustWrite(t, filepath.Join(root, "tools", "figures", "dist", "player.js"), "// operator edit\n")
	setDocumentationFacet(t, root, false)
	opts := goldenAdoptOptions(t, root)
	opts.Force = true
	golden.adopt("disable over a drifted figure asset", root, opts)
	testsupport.AssertGolden(t, filepath.Join("testdata", "managed-family", "figure-foreign.golden"), golden.sb.String())
}

// Positive: the attribute block goes to the tail of a new or existing file, CRLF preserved, and
// stripping it keeps the operator's lines; the documentation families declare exactly the figure
// engine's rules.
func TestMergeGitAttributesPositive(t *testing.T) {
	rules := DocumentationAttributes()
	if block := ManagedGitAttributesBlock(rules); block != figureAttributeBlock {
		t.Fatalf("attribute block = %q, want %q", block, figureAttributeBlock)
	}
	for text, want := range map[string]string{
		"":                             figureAttributeBlock,
		"* text=auto\n":                "* text=auto\n\n" + figureAttributeBlock,
		figureAttributeBlock + "a b\n": "a b\n\n" + figureAttributeBlock,
		"* text=auto\r\n":              strings.ReplaceAll("* text=auto\n\n"+figureAttributeBlock, "\n", "\r\n"),
	} {
		merged, err := mergeGitAttributes(text, rules)
		if err != nil || merged != want {
			t.Fatalf("merge(%q) = %q, %v; want %q", text, merged, err, want)
		}
	}
	stripped, err := mergeGitAttributes("* text=auto\n\n"+figureAttributeBlock, nil)
	if err != nil || stripped != "* text=auto\n" {
		t.Fatalf("strip = %q, %v", stripped, err)
	}
}

// Negative: ambiguous markers are refused when merging and when stripping, and the audit probe
// reports them as errors rather than as a present or absent block.
func TestMergeGitAttributesNegative(t *testing.T) {
	for _, text := range []string{
		figureAttributeBlock + figureAttributeBlock,
		"# BEGIN praetor managed attributes (praetorctl adopt)\n",
		"# END praetor managed attributes\n",
		" # BEGIN praetor managed attributes (praetorctl adopt)\n",
		"a\r\nb\n",
	} {
		for _, rules := range [][]string{DocumentationAttributes(), nil} {
			if _, err := mergeGitAttributes(text, rules); err == nil || !strings.HasPrefix(err.Error(), ".gitattributes ") {
				t.Fatalf("merge(%q, %d rules) = %v, want a .gitattributes refusal", text, len(rules), err)
			}
		}
		if _, err := GitAttributesBlockPresent(text); err == nil {
			t.Fatalf("GitAttributesBlockPresent(%q) accepted ambiguous markers", text)
		}
	}
}

// Boundary: a file at the line bound merges and one more line is refused; with no rules a file
// without a block is returned unchanged and no block renders; the probe tells a block from the
// operator's own lookalike rules.
func TestMergeGitAttributesBoundary(t *testing.T) {
	bound := strings.Repeat("x\n", maxGitAttributesLines-1)
	if _, err := mergeGitAttributes(bound, DocumentationAttributes()); err != nil {
		t.Fatalf("a file at the line bound was refused: %v", err)
	}
	if _, err := mergeGitAttributes(bound+"x\n", DocumentationAttributes()); err == nil {
		t.Fatal("a file above the line bound merged")
	}
	operator := "tools/figures/** text eol=lf\n"
	if stripped, err := mergeGitAttributes(operator, nil); err != nil || stripped != operator {
		t.Fatalf("strip without a block = %q, %v", stripped, err)
	}
	if ManagedGitAttributesBlock(nil) != "" {
		t.Fatal("no rules rendered a block")
	}
	for text, want := range map[string]bool{operator: false, operator + "\n" + figureAttributeBlock: true} {
		if present, err := GitAttributesBlockPresent(text); err != nil || present != want {
			t.Fatalf("GitAttributesBlockPresent(%q) = %v, %v", text, present, err)
		}
	}
}

// Positive, negative and boundary for the adoption step: it creates the file, then verifies it;
// a disable of a file that held only the block deletes the file and one that held operator rules
// keeps them; a dry run reports without writing; a repository without the file and without rules
// is left alone.
func TestReconcileGitAttributes(t *testing.T) {
	s := familySession(t, false)
	rules := DocumentationAttributes()
	if err := reconcileGitAttributes(t.Context(), s, rules); err != nil {
		t.Fatal(err)
	}
	if got, ok := familyFile(t, s, gitAttributesFile); !ok || got != figureAttributeBlock {
		t.Fatalf(".gitattributes = %q (exists %v)", got, ok)
	}
	if created, _ := actionOf(s.report, gitAttributesFile); created.Action != actionCreate {
		t.Fatalf("create action = %+v", created)
	}
	s.report = newAdoptionReport(s.repoPath, s.opts, classify.Result{Archetype: "app-service"})
	if err := reconcileGitAttributes(t.Context(), s, rules); err != nil {
		t.Fatal(err)
	}
	if verified, _ := actionOf(s.report, gitAttributesFile); verified.Details != "Managed attribute block already at the tail" {
		t.Fatalf("rerun action = %+v", verified)
	}
	s.opts.DryRun = true
	if err := reconcileGitAttributes(t.Context(), s, nil); err != nil {
		t.Fatal(err)
	}
	if got, ok := familyFile(t, s, gitAttributesFile); !ok || got != figureAttributeBlock {
		t.Fatal("a dry-run disable changed .gitattributes")
	}
	s.opts.DryRun = false
	if err := reconcileGitAttributes(t.Context(), s, nil); err != nil {
		t.Fatal(err)
	}
	if _, ok := familyFile(t, s, gitAttributesFile); ok {
		t.Fatal("a .gitattributes that held only the block survived the disable")
	}
	if err := reconcileGitAttributes(t.Context(), s, nil); err != nil {
		t.Fatal(err)
	}
	if _, ok := familyFile(t, s, gitAttributesFile); ok {
		t.Fatal("a disable created .gitattributes")
	}
	mustWrite(t, filepath.Join(s.repoPath, gitAttributesFile), "* text=auto\n\n"+figureAttributeBlock)
	if err := reconcileGitAttributes(t.Context(), s, nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := familyFile(t, s, gitAttributesFile); got != "* text=auto\n" {
		t.Fatalf("disable kept %q, want the operator rule alone", got)
	}
}

// priorDocumentationBlock is the Markdown-only block an earlier Praetor wrote, spelled out.
const priorDocumentationBlock = "# BEGIN praetor documentation gate\n.PHONY: docs-lint\nverify-all: docs-lint\n" +
	"docs-lint:\n\t@node tools/markdownlint/verify.mjs\n# END praetor documentation gate\n"

// Positive: the managed block attaches docs-figures, running the engine's check and sources,
// beside docs-lint; the earlier Markdown-only block is refreshed without --force, in place, and
// removed on disable like the current one (the report entry of the refresh:
// TestReconcileDocumentationMakefile_Positive_PriorBlockIsRefreshed).
func TestDocumentationMakefileFigureTargetPositive(t *testing.T) {
	block := DocumentationMakefileBlock()
	for _, want := range []string{
		"verify-all: docs-lint docs-figures\n",
		"docs-figures:\n\t@node tools/figures/build.mjs check\n\t@node tools/figures/build.mjs sources\n",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("the documentation block lacks %q:\n%s", want, block)
		}
	}
	existing := "all:\n\t@true\n\n" + priorDocumentationBlock + "\nextra:\n\t@true\n"
	merged, err := mergeDocumentationMakefile(existing, false)
	if err != nil || merged != "all:\n\t@true\n\n"+block+"\nextra:\n\t@true\n" {
		t.Fatalf("the earlier block was not replaced in place: %v\n%s", err, merged)
	}
	removed, ok, err := removeDocumentationMakefileBlock(existing)
	if err != nil || !ok || removed != "all:\n\t@true\nextra:\n\t@true\n" {
		t.Fatalf("the earlier block was not removed: ok=%v err=%v\n%s", ok, err, removed)
	}
}

// Negative: an edited earlier block keeps the --force contract and is not removed, and a
// docs-figures target the operator defines outside the block stops the merge.
func TestDocumentationMakefileFigureTargetNegative(t *testing.T) {
	edited := strings.Replace(priorDocumentationBlock, "@node", "@npx", 1)
	if _, err := mergeDocumentationMakefile(edited, false); err == nil {
		t.Fatal("an edited earlier block was replaced without --force")
	}
	if _, _, err := removeDocumentationMakefileBlock(edited); err == nil {
		t.Fatal("an edited earlier block was removed")
	}
	_, err := mergeDocumentationMakefile("docs-figures:\n\t@echo operator\n", false)
	if err == nil || !strings.Contains(err.Error(), "may define target docs-figures") {
		t.Fatalf("an operator docs-figures target was not refused: %v", err)
	}
}

// Boundary: the earlier block as the file's last lines, without a line after its end marker, is
// not an exact Praetor block, as the current one is not; --force still repairs it.
func TestDocumentationMakefileFigureTargetBoundary(t *testing.T) {
	unterminated := strings.TrimSuffix(priorDocumentationBlock, "\n")
	if _, err := mergeDocumentationMakefile(unterminated, false); err == nil {
		t.Fatal("an earlier block without its final newline was replaced without --force")
	}
	repaired, err := mergeDocumentationMakefile(unterminated, true)
	if err != nil || !strings.HasPrefix(repaired, strings.TrimSuffix(DocumentationMakefileBlock(), "\n")) {
		t.Fatalf("--force did not repair the earlier block: %v\n%s", err, repaired)
	}
	if !slices.Contains(documentationMakefileTargets, "docs-figures") || len(priorDocumentationMakefileBlocks) != 1 {
		t.Fatalf("targets %v, earlier blocks %d", documentationMakefileTargets, len(priorDocumentationMakefileBlocks))
	}
}
