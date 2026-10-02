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
// managed attribute block carries the rule that keeps a checkout from converting that file
// (#313). These tests pin the rule, the block it joins, what adoption keeps and refuses of an
// operator's own rules, that the operator's files beside the bundle keep their attributes, and
// that a CRLF checkout verifies once the rule exists and the remedy's commands ran.

// devContainerAttributeBlock is the block adoption writes while no documentation family is
// enabled, spelled out.
const devContainerAttributeBlock = "# BEGIN praetor managed attributes (praetorctl adopt)\n" +
	"# praetorctl audit hashes these files; keep their checkout bytes the same on every platform.\n" +
	".devcontainer/Dockerfile.praetor text eol=lf\n" +
	"# END praetor managed attributes\n"

// fullAttributeBlock is the block adoption writes with docs:seo-portal enabled, spelled out:
// the DevContainer rule ahead of the figure engine's.
var fullAttributeBlock = strings.Replace(figureAttributeBlock, "tools/figures/** text eol=lf\n",
	".devcontainer/Dockerfile.praetor text eol=lf\ntools/figures/** text eol=lf\n", 1)

// Positive: the block holds the DevContainer rule ahead of the documentation rules; merging it
// keeps every operator line, the operator's own rules on the directory included, and is
// idempotent.
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
		"":                              devContainerAttributeBlock,
		"* text=auto\n":                 "* text=auto\n\n" + devContainerAttributeBlock,
		".devcontainer/* text eol=lf\n": ".devcontainer/* text eol=lf\n\n" + devContainerAttributeBlock,
		".devcontainer/Dockerfile.praetor text eol=lf\n": ".devcontainer/Dockerfile.praetor text eol=lf\n\n" + devContainerAttributeBlock,
		"*.json eol=crlf\r\n":                            strings.ReplaceAll("*.json eol=crlf\n\n"+devContainerAttributeBlock, "\n", "\r\n"),
		figureAttributeBlock + "a b\n":                   "a b\n\n" + devContainerAttributeBlock,
		"a b\n\n" + fullAttributeBlock:                   "a b\n\n" + devContainerAttributeBlock,
		devContainerAttributeBlock + "a":                 "a\n\n" + devContainerAttributeBlock,
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

// Negative: an operator rule inside the DevContainer directory that can match the pinned
// Dockerfile and contradicts "text eol=lf" is refused, with the rule, the file and the managed
// rule in the message, whatever else the file holds; nothing is merged.
func TestManagedAttributes_Negative_ConflictingOperatorRuleRefused(t *testing.T) {
	for _, line := range []string{
		".devcontainer/* -text",
		".devcontainer/* text eol=crlf",
		".devcontainer/** binary",
		"/.devcontainer/Dockerfile.praetor eol=crlf",
		".devcontainer/*.praetor !text",
		".devcontainer/* !eol",
		".devcontainer/* -eol",
		".devcontainer/Dockerfile* text=auto eol=crlf",
		".devcontainer/**/Dockerfile.praetor -text",
		".devcontainer/**/**/* binary",
		".devcontainer/Dockerfile.praeto[r] binary",
		".devcontainer/Dockerfile.praeto? -text",
		`".devcontainer/*" -text`,
		"\t.devcontainer/Dockerfile.praetor  diff  eol=crlf \t ",
	} {
		for _, text := range []string{line + "\n", "* text=auto\n" + line + "\n\n" + devContainerAttributeBlock, "* text=auto\r\n" + line + "\r\n"} {
			merged, err := mergeGitAttributes(text, ManagedAttributes(true, true))
			if err == nil || merged != "" {
				t.Fatalf("merge(%q) = %q, want a refusal", text, merged)
			}
			for _, want := range []string{
				".gitattributes rule ", strings.TrimSpace(line), "gives .devcontainer/Dockerfile.praetor another line-ending treatment",
				`managed rule ".devcontainer/Dockerfile.praetor text eol=lf"`, "dev-container in adoption.decline",
			} {
				if !strings.Contains(err.Error(), want) && !strings.Contains(err.Error(), strings.ReplaceAll(want, `"`, `\"`)) {
					t.Fatalf("refusal of %q lacks %q: %v", line, want, err)
				}
			}
		}
	}
}

// Boundary: only a rule inside the DevContainer directory that can match the pinned Dockerfile
// and contradicts its line-ending treatment is a conflict. The operator's repository-wide
// defaults, a rule on another file or a subdirectory of the directory, a compatible rule
// (text=auto included: the Dockerfile is text), a rule setting other attributes, a pattern git
// cannot parse, a comment, a lookalike path and a pattern without attributes merge; so does a
// conflicting line while the block carries no DevContainer rule, and one inside the block is an
// edit, not an operator rule.
func TestManagedAttributes_Boundary_OnlyRulesOnThePinnedFileConflict(t *testing.T) {
	for _, line := range []string{
		"* text=auto eol=crlf", "*.json eol=crlf", "* -text", "Dockerfile.praetor eol=crlf", "*.praetor binary", "*.png binary",
		".devcontainer/*.json eol=crlf", ".devcontainer/*.json text=auto", ".devcontainer/setup.ps1 diff eol=crlf",
		".devcontainer/logo.png binary", ".devcontainer/*.png -text", ".devcontainer/scripts/* -text",
		".devcontainer/scripts/** eol=crlf", ".devcontainer/*/Dockerfile.praetor -text", ".devcontainer/Dockerfile.praetor.bak -text",
		".devcontainer/* text eol=lf", ".devcontainer/* text", ".devcontainer/* eol=lf", ".devcontainer/* text=auto",
		".devcontainer/* text=auto eol=lf", ".devcontainer/Dockerfile.praetor text eol=lf",
		".devcontainer/* linguist-generated -diff merge=ours", ".devcontainer/* -binary", ".devcontainer/[ -text",
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

// Positive, negative and boundary for the pattern probe: inside the file's directory a glob
// over the file name matches, with any leading "**/" components; another directory, a further
// directory component, another name and a pattern path.Match cannot parse do not.
func TestAttributePatternMatches(t *testing.T) {
	const file = ".devcontainer/Dockerfile.praetor"
	for pattern, want := range map[string]bool{
		".devcontainer/Dockerfile.praetor": true, ".devcontainer/*": true, ".devcontainer/**": true,
		".devcontainer/Dockerfile*": true, ".devcontainer/*.praetor": true, ".devcontainer/?ockerfile.praetor": true,
		".devcontainer/[A-Z]ockerfile.praetor": true, ".devcontainer/**/*": true, ".devcontainer/**/**/Dockerfile.praetor": true,
		".devcontainer/" + strings.Repeat("**/", maxAttributePatternDepth) + "*":   true,
		".devcontainer/" + strings.Repeat("**/", maxAttributePatternDepth+1) + "*": false,
		".devcontainer/*.json": false, ".devcontainer/dockerfile.praetor": false, ".devcontainer/Dockerfile.praetor.bak": false,
		".devcontainer/sub/*": false, ".devcontainer/*/Dockerfile.praetor": false, ".devcontainer/Dockerfile.praetor/**": false,
		".devcontainer/": false, ".devcontainer": false, ".devcontainer/[": false, "": false,
		"Dockerfile.praetor": false, "*": false, "**/Dockerfile.praetor": false, ".devcontainerx/*": false,
	} {
		if got := attributePatternMatches(pattern, file); got != want {
			t.Fatalf("attributePatternMatches(%q) = %v, want %v", pattern, got, want)
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
// adoption creates the file with the rule; a rerun changes nothing; an operator's rules, one on
// another file of the directory and one after the block included, are kept and the block
// returns to the tail once; declining
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
	mustWrite(t, filepath.Join(root, gitAttributesFile), "* text=auto\n.devcontainer/* text eol=lf\n.devcontainer/setup.ps1 text eol=crlf\n\n"+devContainerAttributeBlock+"*.png binary\n")
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

// crlfCheckoutFixture is a git repository with core.autocrlf=true, git's default on Windows,
// holding the bundle adoption writes. The tests set the option in the fixture repository, so
// they run the same on Linux, macOS and Windows; they are skipped where git is not installed
// (testsupport.InitGitRepoWithOrigin).
type crlfCheckoutFixture struct {
	t      *testing.T
	s      *adoptSession
	config string
}

func newCRLFCheckoutFixture(t *testing.T) *crlfCheckoutFixture {
	t.Helper()
	s := bootstrapAdoptSession(t, adoptBootstrapSource(t), false)
	testsupport.InitGitRepoWithOrigin(t, s.repoPath, "")
	fixtureGit(t, s.repoPath, "config", "core.autocrlf", "true")
	if err := reconcileDevContainer(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	return &crlfCheckoutFixture{t: t, s: s, config: filepath.Join(s.repoPath, filepath.FromSlash(devcontainerFile))}
}

// file is the path of the bundle directory's file name.
func (f *crlfCheckoutFixture) file(name string) string {
	return filepath.Join(filepath.Dir(f.config), name)
}

func (f *crlfCheckoutFixture) commit() {
	f.t.Helper()
	fixtureGit(f.t, f.s.repoPath, "add", "-A")
	fixtureGit(f.t, f.s.repoPath, "commit", "--quiet", "-m", "fixture")
}

// clone writes the committed bundle directory as a fresh clone would: the files are gone, so
// git writes each of them under the attributes in force.
func (f *crlfCheckoutFixture) clone() {
	f.t.Helper()
	if err := os.RemoveAll(filepath.Dir(f.config)); err != nil {
		f.t.Fatal(err)
	}
	fixtureGit(f.t, f.s.repoPath, "checkout", "--", filepath.Base(filepath.Dir(f.config)))
}

// verify verifies the bundle against the configuration adoption synthesizes.
func (f *crlfCheckoutFixture) verify() error {
	f.t.Helper()
	expected, err := devcontainer.Synthesize(adoptionManifest(f.t, f.s))
	if err != nil {
		f.t.Fatal(err)
	}
	return devcontainer.Verify(f.t.Context(), f.config, expected)
}

// Positive, with git: a CRLF checkout verifies once the rule exists and the remedy's commands
// ran. Under core.autocrlf=true a clone converts Dockerfile.praetor and verification fails as a
// checkout conversion. After adoption wrote the attribute block, the working tree that
// predates the rule is written again by the commands the remedy names
// (devcontainer.RecheckoutCommands), with no file deleted by hand, and verifies; a clone under
// the rule verifies as it is.
func TestDevContainerAttribute_Positive_CRLFCheckoutVerifiesOnceTheRuleExists(t *testing.T) {
	fixture := newCRLFCheckoutFixture(t)
	dockerfile := fixture.file("Dockerfile.praetor")
	fixture.commit()
	fixture.clone()
	if text := mustRead(t, dockerfile); !strings.Contains(text, "\r\n") {
		t.Fatalf("fixture: core.autocrlf=true did not convert the checkout: %q", text[:min(len(text), 80)])
	}
	if err := fixture.verify(); !errors.Is(err, devcontainer.ErrCheckoutLineEndings) {
		t.Fatalf("a CRLF checkout without the rule: %v", err)
	}
	if err := reconcileManagedAttributes(t.Context(), fixture.s, false); err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, filepath.Join(fixture.s.repoPath, gitAttributesFile)); got != devContainerAttributeBlock {
		t.Fatalf(".gitattributes = %q", got)
	}
	fixture.commit()
	for _, command := range devcontainer.RecheckoutCommands() {
		fixtureGit(t, fixture.s.repoPath, command...)
	}
	if text := mustRead(t, dockerfile); strings.Contains(text, "\r") {
		t.Fatalf("the remedy's commands left the working tree converted: %q", text[:min(len(text), 80)])
	}
	if err := fixture.verify(); err != nil {
		t.Fatalf("the working tree after the remedy's commands: %v", err)
	}
	if status := fixtureGit(t, fixture.s.repoPath, "status", "--porcelain"); strings.TrimSpace(status) != "" {
		t.Fatalf("the remedy's commands left changes behind: %q", status)
	}
	fixture.clone()
	if text := mustRead(t, dockerfile); strings.Contains(text, "\r") {
		t.Fatalf("the rule did not keep a clone LF: %q", text[:min(len(text), 80)])
	}
	if err := fixture.verify(); err != nil {
		t.Fatalf("a clone under the rule: %v", err)
	}
}

// Negative for the rule's reach, with git: the block at the tail changes the attributes of the
// pinned Dockerfile alone. An image and a CRLF script the operator keeps beside the bundle
// stay under the operator's own rules, the image keeps the CRLF byte pairs every PNG signature
// holds through git add and a clone, and the operator's rules stay in the file. A wildcard rule
// on the directory made the image text and rewrote those bytes on the next git add.
func TestDevContainerAttribute_Negative_OperatorFilesBesideTheBundleKeepTheirBytes(t *testing.T) {
	fixture := newCRLFCheckoutFixture(t)
	const image, script = "\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\r\nrow\r\n", "Write-Host 'ready'\r\n"
	operatorRules := "*.png binary\n*.ps1 text eol=crlf\n"
	mustWrite(t, fixture.file("logo.png"), image)
	mustWrite(t, fixture.file("setup.ps1"), script)
	mustWrite(t, filepath.Join(fixture.s.repoPath, gitAttributesFile), operatorRules)
	fixture.commit()
	if err := reconcileManagedAttributes(t.Context(), fixture.s, false); err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, filepath.Join(fixture.s.repoPath, gitAttributesFile)); got != operatorRules+"\n"+devContainerAttributeBlock {
		t.Fatalf(".gitattributes = %q", got)
	}
	attributes := fixtureGit(t, fixture.s.repoPath, "check-attr", "text", "eol", "--",
		".devcontainer/logo.png", ".devcontainer/setup.ps1", ".devcontainer/devcontainer.json", devcontainer.CheckoutPinnedFile)
	for _, want := range []string{
		".devcontainer/logo.png: text: unset", ".devcontainer/logo.png: eol: unspecified",
		".devcontainer/setup.ps1: text: set", ".devcontainer/setup.ps1: eol: crlf",
		".devcontainer/devcontainer.json: text: unspecified",
		".devcontainer/Dockerfile.praetor: text: set", ".devcontainer/Dockerfile.praetor: eol: lf",
	} {
		if !strings.Contains(attributes, want) {
			t.Fatalf("git check-attr lacks %q:\n%s", want, attributes)
		}
	}
	if status := fixtureGit(t, fixture.s.repoPath, "status", "--porcelain"); strings.TrimSpace(status) != "?? .gitattributes" &&
		strings.TrimSpace(status) != "M .gitattributes" {
		t.Fatalf("the block changed more than .gitattributes: %q", status)
	}
	fixture.commit()
	fixture.clone()
	if got := mustRead(t, fixture.file("logo.png")); got != image {
		t.Fatalf("the image changed under the block: %q", got)
	}
	if got := mustRead(t, fixture.file("setup.ps1")); got != script {
		t.Fatalf("the operator's script changed under the block: %q", got)
	}
	if err := fixture.verify(); err != nil {
		t.Fatalf("the bundle beside the operator's files: %v", err)
	}
}

// Negative and positive through Adopt, without the documentation facet: the block then holds
// the DevContainer rule alone and the documentation gate runs its disable path on every run. An
// edited block stops a plain run before its first write and names the forced rerun; --force
// restores the block as a replace with its line delta and a backup, and removes nothing else
// of the operator's.
func TestAdopt_EditedAttributeBlockWithoutDocumentationFacet(t *testing.T) {
	root := newTestRepo(t, "devcontainer-attribute-edited")
	opts := goldenAdoptOptions(t, root)
	opts.Facets, opts.SetFacets = []string{"security:high"}, true
	if _, err := Adopt(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
	attributes := filepath.Join(root, gitAttributesFile)
	edited := "* text=auto\n\n" + strings.Replace(devContainerAttributeBlock, "# END", "*.png binary\n# END", 1)
	mustWrite(t, attributes, edited)
	fixtureGit(t, root, "add", "-A")
	before := fixtureGit(t, root, "status", "--porcelain", "--untracked-files=all")
	_, err := Adopt(t.Context(), opts)
	if err == nil || !strings.Contains(err.Error(), ".gitattributes managed attribute block was edited; review it and rerun "+ForceCommand(opts.LockSourceRoot)) ||
		strings.Contains(err.Error(), "documentation disable") {
		t.Fatalf("a plain run over an edited block: %v", err)
	}
	if after := fixtureGit(t, root, "status", "--porcelain", "--untracked-files=all"); after != before || mustRead(t, attributes) != edited {
		t.Fatalf("the refused run wrote:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	forced := opts
	forced.Force = true
	report, err := Adopt(t.Context(), forced)
	if err != nil {
		t.Fatalf("a forced run over an edited block: %v", err)
	}
	if got := mustRead(t, attributes); got != "* text=auto\n\n"+devContainerAttributeBlock {
		t.Fatalf("the forced run left .gitattributes %q", got)
	}
	if entry := replacedEntry(t, report, gitAttributesFile); !strings.HasPrefix(entry.Details,
		"Restored the managed attribute block at the tail; replaced existing content (-1/+0 lines, removed \"*.png binary\")") {
		t.Fatalf("replace entry = %+v", entry)
	}
	if _, err := Adopt(t.Context(), opts); err != nil {
		t.Fatalf("a plain run after the forced one: %v", err)
	}
}

// Boundary for the two preflights over an edited block. The adoption preflight refuses it
// without --force, whatever the decline list says, and passes it with --force. The disable
// preflight follows the rules of the run: with the DevContainer rule kept the block is
// rewritten, so --force is what it asks for; with dev-container declined the block would be
// removed, which an edited block refuses under --force too.
func TestPreflightAttributes_Boundary_EditedBlock(t *testing.T) {
	edited := "* text=auto\n\n" + strings.Replace(devContainerAttributeBlock, "# END", "*.png binary\n# END", 1)
	session := func(force bool, declines ...string) *adoptSession {
		s := backupSession(t, map[string]string{gitAttributesFile: edited}, true, AdoptOptions{Force: force})
		s.declined = declines
		return s
	}
	plain := session(false)
	for name, err := range map[string]error{
		"adoption preflight":           preflightManagedAttributes(t.Context(), plain, nil),
		"adoption preflight, declined": preflightManagedAttributes(t.Context(), plain, map[string]bool{devContainerStep: true}),
		"disable preflight":            preflightDocumentationAttributes(t.Context(), plain),
	} {
		if err == nil || !strings.Contains(err.Error(), "managed attribute block was edited; review it and rerun "+plain.forceCommand()) {
			t.Fatalf("%s without --force: %v", name, err)
		}
	}
	forced := session(true)
	for name, err := range map[string]error{
		"adoption preflight":            preflightManagedAttributes(t.Context(), forced, nil),
		"adoption preflight, declined":  preflightManagedAttributes(t.Context(), forced, map[string]bool{devContainerStep: true}),
		"disable preflight, rule kept":  preflightDocumentationAttributes(t.Context(), forced),
		"adoption preflight, unedited":  preflightManagedAttributes(t.Context(), familySession(t, false), nil),
		"disable preflight, no file":    preflightDocumentationAttributes(t.Context(), familySession(t, false)),
		"adoption preflight, no block ": preflightManagedAttributes(t.Context(), backupSession(t, map[string]string{gitAttributesFile: "* text=auto\n"}, true, AdoptOptions{}), nil),
	} {
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	for _, force := range []bool{false, true} {
		declined := session(force, devContainerStep)
		err := preflightDocumentationAttributes(t.Context(), declined)
		if err == nil || !strings.Contains(err.Error(), ".gitattributes blocks documentation disable: refusing to remove the edited managed attribute block") {
			t.Fatalf("disable preflight with dev-container declined, force %v: %v", force, err)
		}
	}
	unknown := session(true, "no-such-step")
	if err := preflightDocumentationAttributes(t.Context(), unknown); err == nil {
		t.Fatal("the disable preflight accepted an unknown decline")
	}
}
