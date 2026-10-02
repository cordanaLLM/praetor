// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/devcontainer"
	"github.com/cordanaLLM/praetor/internal/testsupport"
)

// Adoption writes .devcontainer/ and audit compares Dockerfile.praetor as raw bytes, so the
// managed attribute block carries the rule that keeps a checkout from converting the directory
// (#313). These tests pin the rule, the block it joins, what adoption keeps and refuses of an
// operator's own rules, and that a CRLF checkout verifies once the rule exists.

// devContainerAttributeBlock is the block adoption writes while no documentation family is
// enabled, spelled out.
const devContainerAttributeBlock = "# BEGIN praetor managed attributes (praetorctl adopt)\n" +
	"# praetorctl audit hashes these files; keep their checkout bytes the same on every platform.\n" +
	".devcontainer/* text eol=lf\n" +
	"# END praetor managed attributes\n"

// fullAttributeBlock is the block adoption writes with docs:seo-portal enabled, spelled out:
// the DevContainer rule ahead of the figure engine's.
var fullAttributeBlock = strings.Replace(figureAttributeBlock, "tools/figures/** text eol=lf\n",
	".devcontainer/* text eol=lf\ntools/figures/** text eol=lf\n", 1)

// Positive: the block holds the DevContainer rule ahead of the documentation rules; merging it
// keeps every operator line, the operator's own copy of the rule included, and is idempotent.
func TestManagedAttributes_Positive(t *testing.T) {
	for want, rules := range map[string][]string{
		devContainerAttributeBlock: ManagedAttributes(true, false),
		fullAttributeBlock:         ManagedAttributes(true, true),
		figureAttributeBlock:       ManagedAttributes(false, true),
	} {
		if block := ManagedGitAttributesBlock(rules); block != want {
			t.Fatalf("block of %q = %q, want %q", rules, block, want)
		}
	}
	rules := ManagedAttributes(true, false)
	for text, want := range map[string]string{
		"":                               devContainerAttributeBlock,
		"* text=auto\n":                  "* text=auto\n\n" + devContainerAttributeBlock,
		".devcontainer/* text eol=lf\n":  ".devcontainer/* text eol=lf\n\n" + devContainerAttributeBlock,
		"*.json eol=crlf\r\n":            strings.ReplaceAll("*.json eol=crlf\n\n"+devContainerAttributeBlock, "\n", "\r\n"),
		figureAttributeBlock + "a b\n":   "a b\n\n" + devContainerAttributeBlock,
		"a b\n\n" + fullAttributeBlock:   "a b\n\n" + devContainerAttributeBlock,
		devContainerAttributeBlock + "a": "a\n\n" + devContainerAttributeBlock,
	} {
		merged, err := mergeGitAttributes(text, rules)
		if err != nil || merged != want {
			t.Fatalf("merge(%q) = %q, %v; want %q", text, merged, err, want)
		}
		if again, err := mergeGitAttributes(merged, rules); err != nil || again != merged {
			t.Fatalf("a second merge of %q changed it: %q, %v", text, again, err)
		}
	}
}

// Negative: an operator rule that names the DevContainer directory and gives text or eol
// another state is refused, with the rule and the managed rule in the message, whatever else
// the file holds; nothing is merged.
func TestManagedAttributes_Negative_ConflictingOperatorRuleRefused(t *testing.T) {
	for line, named := range map[string]string{
		".devcontainer/* -text":                         ".devcontainer/* -text",
		".devcontainer/* text eol=crlf":                 ".devcontainer/* text eol=crlf",
		".devcontainer/** binary":                       ".devcontainer/** binary",
		"/.devcontainer/Dockerfile.praetor eol=crlf":    "/.devcontainer/Dockerfile.praetor eol=crlf",
		".devcontainer/*.json text=auto":                ".devcontainer/*.json text=auto",
		".devcontainer/* !eol":                          ".devcontainer/* !eol",
		".devcontainer/* -eol":                          ".devcontainer/* -eol",
		".devcontainer/* !text":                         ".devcontainer/* !text",
		`".devcontainer/*" -text`:                       `".devcontainer/*" -text`,
		"\t.devcontainer/setup.ps1  diff  eol=crlf \t ": ".devcontainer/setup.ps1  diff  eol=crlf",
	} {
		for _, text := range []string{line + "\n", "* text=auto\n" + line + "\n\n" + devContainerAttributeBlock, "* text=auto\r\n" + line + "\r\n"} {
			merged, err := mergeGitAttributes(text, ManagedAttributes(true, true))
			if err == nil || merged != "" {
				t.Fatalf("merge(%q) = %q, want a refusal", text, merged)
			}
			for _, want := range []string{".gitattributes rule ", strings.TrimSpace(named), `managed rule ".devcontainer/* text eol=lf"`, "dev-container in adoption.decline"} {
				if !strings.Contains(err.Error(), want) && !strings.Contains(err.Error(), strings.ReplaceAll(want, `"`, `\"`)) {
					t.Fatalf("refusal of %q lacks %q: %v", line, want, err)
				}
			}
		}
	}
}

// Boundary: only a rule on the DevContainer directory that changes text or eol is a conflict.
// The operator's repository-wide defaults, a compatible rule, a rule setting other attributes,
// a comment, a lookalike path and a pattern without attributes merge; so does a conflicting
// line while the block carries no DevContainer rule, and one inside the block is an edit, not
// an operator rule.
func TestManagedAttributes_Boundary_OnlyDevContainerLineEndingRulesConflict(t *testing.T) {
	for _, line := range []string{
		"* text=auto eol=crlf", "*.json eol=crlf", "* -text", "Dockerfile.praetor eol=crlf",
		".devcontainer/* text eol=lf", ".devcontainer/* text", ".devcontainer/* eol=lf",
		".devcontainer/* linguist-generated -diff merge=ours", ".devcontainer/* -binary",
		"# .devcontainer/* -text", ".devcontainer -text", ".devcontainerx/* -text",
		"docs/.devcontainer/* -text", ".devcontainer/*", "",
	} {
		merged, err := mergeGitAttributes(line+"\n", ManagedAttributes(true, false))
		if err != nil || !strings.HasSuffix(merged, devContainerAttributeBlock) || !strings.HasPrefix(merged, line) {
			t.Fatalf("merge(%q) = %q, %v", line, merged, err)
		}
	}
	conflict := ".devcontainer/* -text\n"
	if merged, err := mergeGitAttributes(conflict, ManagedAttributes(false, true)); err != nil || merged != conflict+"\n"+figureAttributeBlock {
		t.Fatalf("without the DevContainer rule the operator rule conflicts with nothing: %q, %v", merged, err)
	}
	inBlock := strings.Replace(devContainerAttributeBlock, "text eol=lf", "-text", 1)
	if _, err := mergeGitAttributes(inBlock, ManagedAttributes(true, false)); err != nil {
		t.Fatalf("a rule inside the block is not an operator rule: %v", err)
	}
	if edited, err := gitAttributesBlockEdited(inBlock); err != nil || !edited {
		t.Fatalf("a changed rule inside the block is an edit: %v, %v", edited, err)
	}
	for _, text := range []string{
		"a b\n\n" + devContainerAttributeBlock, "a b\n\n" + fullAttributeBlock, "a b\n\n" + figureAttributeBlock,
		strings.ReplaceAll("a b\n\n"+fullAttributeBlock, "\n", "\r\n"),
	} {
		if edited, err := gitAttributesBlockEdited(text); err != nil || edited {
			t.Fatalf("a block adoption writes counts as edited: %q, %v", text, err)
		}
	}
}

// Positive: the documentation-only block every earlier release wrote is refreshed to the rules
// of this run without --force and without a backup, and so is the block left by a toggled facet
// or decline; the report says it was refreshed.
func TestReconcileGitAttributes_Positive_EarlierBlockRefreshedWithoutForce(t *testing.T) {
	for before, after := range map[string]string{
		figureAttributeBlock:       fullAttributeBlock,
		devContainerAttributeBlock: fullAttributeBlock,
	} {
		s := backupSession(t, map[string]string{gitAttributesFile: "* text=auto\n\n" + before}, true, AdoptOptions{})
		if err := reconcileManagedAttributes(t.Context(), s, true); err != nil {
			t.Fatal(err)
		}
		if got := mustRead(t, filepath.Join(s.repoPath, gitAttributesFile)); got != "* text=auto\n\n"+after {
			t.Fatalf(".gitattributes = %q", got)
		}
		action, _ := actionOf(s.report, gitAttributesFile)
		if action.Action != actionAppend || !strings.Contains(action.Details, "refreshed the managed attribute block") ||
			len(s.report.Replaced()) != 0 || fileExists(backupFile(s, gitAttributesFile)) {
			t.Fatalf("refresh of an earlier block: %+v", s.report.ActionDetails)
		}
	}
}

// Negative: an edited block is still refused without --force when the run changes the block's
// rules, and the file stays as it is.
func TestReconcileGitAttributes_Negative_EditedBlockRefusedOnRefresh(t *testing.T) {
	edited := "* text=auto\n\n" + strings.Replace(devContainerAttributeBlock, "# END", "*.png binary\n# END", 1)
	s := backupSession(t, map[string]string{gitAttributesFile: edited}, true, AdoptOptions{})
	err := reconcileManagedAttributes(t.Context(), s, true)
	if err == nil || !strings.Contains(err.Error(), "managed attribute block was edited") {
		t.Fatalf("an edited block: %v", err)
	}
	if got := mustRead(t, filepath.Join(s.repoPath, gitAttributesFile)); got != edited {
		t.Fatalf("the refused .gitattributes changed: %q", got)
	}
}

// Boundary: adoption.decline decides whether the DevContainer rule is in the block. Declined,
// an enabled documentation facet keeps the documentation-only block, a disabled one removes the
// block and keeps the operator's lines; an unknown decline fails instead of writing.
func TestReconcileManagedAttributes_Boundary_DeclineDropsTheRule(t *testing.T) {
	s := backupSession(t, map[string]string{gitAttributesFile: "* text=auto\n\n" + fullAttributeBlock}, true, AdoptOptions{})
	s.declined = []string{devContainerStep}
	if err := reconcileManagedAttributes(t.Context(), s, true); err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, filepath.Join(s.repoPath, gitAttributesFile)); got != "* text=auto\n\n"+figureAttributeBlock {
		t.Fatalf("declined, documentation enabled: %q", got)
	}
	if err := reconcileManagedAttributes(t.Context(), s, false); err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, filepath.Join(s.repoPath, gitAttributesFile)); got != "* text=auto\n" {
		t.Fatalf("declined, documentation disabled: %q", got)
	}
	removal := s.report.ActionDetails[len(s.report.ActionDetails)-1]
	if removal.Path != gitAttributesFile || removal.Action != actionRemove ||
		!strings.Contains(removal.Details, "docs:seo-portal is disabled and dev-container is declined") {
		t.Fatalf("removal entry: %+v", removal)
	}
	s.declined = nil
	if err := reconcileManagedAttributes(t.Context(), s, false); err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, filepath.Join(s.repoPath, gitAttributesFile)); got != "* text=auto\n\n"+devContainerAttributeBlock {
		t.Fatalf("decline removed: %q", got)
	}
	s.declined = []string{"no-such-step"}
	if err := reconcileManagedAttributes(t.Context(), s, false); err == nil {
		t.Fatal("an unknown decline wrote the block")
	}
}

// Positive, negative and boundary for the audit probe: the block adoption writes passes with
// and without the DevContainer rule, in LF and CRLF, and no block passes where no documentation
// rule is required; a block of the other facet state, one that is not at the tail and a missing
// one fail; ambiguous markers and mixed line endings are errors.
func TestGitAttributesCanonical(t *testing.T) {
	for _, test := range []struct {
		text          string
		documentation bool
		want          bool
	}{
		{"* text=auto\n\n" + fullAttributeBlock, true, true},
		{"* text=auto\n\n" + figureAttributeBlock, true, true},
		{strings.ReplaceAll("* text=auto\n\n"+fullAttributeBlock, "\n", "\r\n"), true, true},
		{"* text=auto\n\n" + devContainerAttributeBlock, false, true},
		{"* text=auto\n", false, true},
		{"", false, true},
		{"* text=auto\n", true, false},
		{"* text=auto\n\n" + devContainerAttributeBlock, true, false},
		{"* text=auto\n\n" + fullAttributeBlock, false, false},
		{"* text=auto\n\n" + figureAttributeBlock, false, false},
		{devContainerAttributeBlock + "* text=auto\n", false, false},
		{fullAttributeBlock + "* text=auto\n", true, false},
		{strings.Replace(devContainerAttributeBlock, "eol=lf", "eol=crlf", 1), false, false},
	} {
		got, err := GitAttributesCanonical(test.text, test.documentation)
		if err != nil || got != test.want {
			t.Fatalf("GitAttributesCanonical(%q, %v) = %v, %v; want %v", test.text, test.documentation, got, err, test.want)
		}
	}
	for _, text := range []string{devContainerAttributeBlock + devContainerAttributeBlock, "# END praetor managed attributes\n", "a\r\nb\n"} {
		if _, err := GitAttributesCanonical(text, false); err == nil || !strings.HasPrefix(err.Error(), ".gitattributes ") {
			t.Fatalf("GitAttributesCanonical(%q) = %v, want a .gitattributes error", text, err)
		}
	}
}

// devContainerAttributeGoldenPaths records the attribute file alone.
var devContainerAttributeGoldenPaths = []string{gitAttributesFile}

// Positive, negative and boundary through Adopt. Without the documentation facet a fresh
// adoption creates the file with the rule; a rerun changes nothing; an operator's rules, one
// after the block included, are kept and the block returns to the tail once; declining
// dev-container removes the block and keeps those rules, and removing the decline restores it; a
// conflicting operator rule fails the run and leaves the file as it is. With the facet, the
// rule opens the block ahead of the documentation rules, and the decline drops it alone.
func TestDevContainerAttributeAdoptionGolden(t *testing.T) {
	golden := &familyGolden{t: t, paths: devContainerAttributeGoldenPaths}
	root := newTestRepo(t, "devcontainer-attribute-lifecycle")
	opts := goldenAdoptOptions(t, root)
	opts.Facets, opts.SetFacets = []string{"security:high"}, true
	golden.adopt("fresh adoption without docs:seo-portal", root, opts)
	golden.adopt("rerun", root, opts)
	mustWrite(t, filepath.Join(root, gitAttributesFile), "* text=auto\n.devcontainer/* text eol=lf\n\n"+devContainerAttributeBlock+"*.png binary\n")
	golden.adopt("operator rules around the block", root, opts)
	golden.adopt("operator rules, rerun", root, opts)
	setAdoptionDeclines(t, root, devContainerStep)
	golden.adopt("dev-container declined", root, opts)
	golden.adopt("declined, rerun", root, opts)
	setAdoptionDeclines(t, root)
	golden.adopt("decline removed", root, opts)
	mustWrite(t, filepath.Join(root, gitAttributesFile), ".devcontainer/* text eol=crlf\n\n"+devContainerAttributeBlock)
	golden.adopt("conflicting operator rule", root, opts)

	documented := newTestRepo(t, "devcontainer-attribute-documented")
	opts = goldenAdoptOptions(t, documented)
	golden.adopt("fresh adoption with docs:seo-portal", documented, opts)
	setAdoptionDeclines(t, documented, devContainerStep)
	golden.adopt("documented, dev-container declined", documented, opts)
	setAdoptionDeclines(t, documented)
	golden.adopt("documented, decline removed", documented, opts)
	testsupport.AssertGolden(t, filepath.Join("testdata", "managed-family", "devcontainer-attribute.golden"), golden.sb.String())
}

// Negative: a first adoption over a conflicting operator rule is refused before anything is
// written, in a dry run too, and names the rule; declining dev-container lets the same
// repository adopt, with the operator's rule untouched and no DevContainer rule written.
func TestAdopt_Negative_ConflictingAttributeRuleStopsBeforeAnyWrite(t *testing.T) {
	conflict := "* text=auto\n.devcontainer/* -text\n"
	for _, dryRun := range []bool{false, true} {
		root := newTestRepo(t, "devcontainer-attribute-conflict")
		mustWrite(t, filepath.Join(root, gitAttributesFile), conflict)
		opts := goldenAdoptOptions(t, root)
		opts.DryRun = dryRun
		_, err := Adopt(t.Context(), opts)
		if err == nil || !strings.Contains(err.Error(), `.gitattributes rule ".devcontainer/* -text"`) {
			t.Fatalf("dry run %v: a conflicting operator rule was not refused: %v", dryRun, err)
		}
		entries, readErr := os.ReadDir(root)
		if readErr != nil {
			t.Fatal(readErr)
		}
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		slices.Sort(names)
		if !slices.Equal(names, []string{".git", gitAttributesFile}) || mustRead(t, filepath.Join(root, gitAttributesFile)) != conflict {
			t.Fatalf("dry run %v: the refused adoption wrote: %v", dryRun, names)
		}
	}
	s := backupSession(t, map[string]string{gitAttributesFile: conflict}, true, AdoptOptions{})
	if err := preflightManagedAttributes(t.Context(), s, map[string]bool{devContainerStep: true}); err != nil {
		t.Fatalf("a declined dev-container step still refuses the operator rule: %v", err)
	}
	if err := preflightManagedAttributes(t.Context(), s, nil); err == nil {
		t.Fatal("the preflight accepted the operator rule")
	}
	s.declined = []string{devContainerStep}
	if err := reconcileManagedAttributes(t.Context(), s, true); err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, filepath.Join(s.repoPath, gitAttributesFile)); got != conflict+"\n"+figureAttributeBlock {
		t.Fatalf("declined: %q", got)
	}
}

// Boundary: the preflight reads what the merge reads. A repository without .gitattributes and
// one with the operator's compatible rules pass; ambiguous block markers and mixed line endings
// are refused before the first write, as the step would refuse them after it.
func TestPreflightManagedAttributes_Boundary(t *testing.T) {
	absent := familySession(t, false)
	if err := preflightManagedAttributes(t.Context(), absent, nil); err != nil {
		t.Fatalf("no .gitattributes: %v", err)
	}
	for text, refused := range map[string]bool{
		"* text=auto\n.devcontainer/* text eol=lf\n": false,
		"* text=auto\n\n" + fullAttributeBlock:       false,
		"# END praetor managed attributes\n":         true,
		"a\r\nb\n":                                   true,
		strings.Repeat("x\n", maxGitAttributesLines): true,
	} {
		s := familySession(t, false)
		mustWrite(t, filepath.Join(s.repoPath, gitAttributesFile), text)
		if err := preflightManagedAttributes(t.Context(), s, nil); (err != nil) != refused {
			t.Fatalf("preflight(%.40q) = %v, want refused %v", text, err, refused)
		}
	}
}

// Positive, with git: a CRLF checkout fixture verifies once the rule exists. Under
// core.autocrlf=true, git's default on Windows, a checkout of the committed bundle converts
// Dockerfile.praetor and verification fails as a checkout conversion; after adoption wrote the
// attribute block, the same checkout keeps the bundle LF and it verifies. The test sets
// core.autocrlf in the fixture repository, so it runs the same on Linux, macOS and Windows; it
// is skipped where git is not installed (testsupport.InitGitRepoWithOrigin).
func TestDevContainerAttribute_Positive_CRLFCheckoutVerifiesOnceTheRuleExists(t *testing.T) {
	s := bootstrapAdoptSession(t, adoptBootstrapSource(t), false)
	testsupport.InitGitRepoWithOrigin(t, s.repoPath, "")
	fixtureGit(t, s.repoPath, "config", "core.autocrlf", "true")
	if err := reconcileDevContainer(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	expected, err := devcontainer.Synthesize(adoptionManifest(t, s))
	if err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(s.repoPath, devcontainerFile)
	dockerfile := filepath.Join(filepath.Dir(config), "Dockerfile.praetor")
	checkout := func() string {
		t.Helper()
		fixtureGit(t, s.repoPath, "add", "-A")
		fixtureGit(t, s.repoPath, "commit", "--quiet", "-m", "fixture")
		if err := os.RemoveAll(filepath.Dir(config)); err != nil {
			t.Fatal(err)
		}
		fixtureGit(t, s.repoPath, "checkout", "--", filepath.Base(filepath.Dir(config)))
		return mustRead(t, dockerfile)
	}
	if text := checkout(); !strings.Contains(text, "\r\n") {
		t.Fatalf("fixture: core.autocrlf=true did not convert the checkout: %q", text[:min(len(text), 80)])
	}
	if err := devcontainer.Verify(t.Context(), config, expected); !errors.Is(err, devcontainer.ErrCheckoutLineEndings) {
		t.Fatalf("a CRLF checkout without the rule: %v", err)
	}
	if err := reconcileManagedAttributes(t.Context(), s, false); err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, filepath.Join(s.repoPath, gitAttributesFile)); got != devContainerAttributeBlock {
		t.Fatalf(".gitattributes = %q", got)
	}
	if text := checkout(); strings.Contains(text, "\r") {
		t.Fatalf("the rule did not keep the checkout LF: %q", text[:min(len(text), 80)])
	}
	if err := devcontainer.Verify(t.Context(), config, expected); err != nil {
		t.Fatalf("a checkout under the rule: %v", err)
	}
}
