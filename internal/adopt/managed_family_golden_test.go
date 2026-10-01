// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/managedasset"
	"github.com/cordanaLLM/praetor/internal/testsupport"
)

// The Markdown gate was generalised into the managed asset family registry as a pure
// refactor. These goldens were recorded from the code before that change and pin what
// adoption reports and writes for the family and for the two tail-block ignore files, so any
// byte of drift in the refactored paths fails here.

// markdownFamilyGoldenPaths are the paths whose adoption output the golden pins, spelled out
// rather than read from the code under test.
var markdownFamilyGoldenPaths = []string{
	".github/workflows/praetor-docs.yml",
	"tools/markdownlint/package.json",
	"tools/markdownlint/package-lock.json",
	"tools/markdownlint/markdownlint-cli2.yaml",
	"tools/markdownlint/verify.mjs",
	"tools/markdownlint/no-private-scratch-links.mjs",
	".gitignore",
	".prettierignore",
}

// quotedGoldenPaths are recorded as their full text; every other path as a digest.
var quotedGoldenPaths = []string{".gitignore", ".prettierignore", ".gitattributes"}

type familyGolden struct {
	t  *testing.T
	sb strings.Builder
	// paths are the paths the golden records; nil records markdownFamilyGoldenPaths.
	paths []string
}

func (g *familyGolden) goldenPaths() []string {
	if g.paths == nil {
		return markdownFamilyGoldenPaths
	}
	return g.paths
}

func (g *familyGolden) adopt(label, root string, opts AdoptOptions) {
	g.t.Helper()
	report, err := Adopt(g.t.Context(), opts)
	fmt.Fprintf(&g.sb, "== %s\n", label)
	// The temporary directories differ on every run: the repository records as <root>, and the
	// lock source a remedy names (ForceCommand) as <lock source>.
	pairs := []string{root, "<root>"}
	if opts.LockSourceRoot != "" {
		pairs = append(pairs, opts.LockSourceRoot, "<lock source>")
	}
	scrub := strings.NewReplacer(pairs...)
	if report != nil {
		g.recordReport(scrub, report)
	}
	if err != nil {
		fmt.Fprintf(&g.sb, "error %s\n", scrub.Replace(err.Error()))
	}
	g.recordFiles(root)
}

func (g *familyGolden) recordReport(scrub *strings.Replacer, report *AdoptReport) {
	for _, detail := range report.ActionDetails {
		if slices.Contains(g.goldenPaths(), detail.Path) {
			fmt.Fprintf(&g.sb, "action %s %s :: %s\n", detail.Action, detail.Path, scrubBackupStamp(scrub.Replace(detail.Details)))
		}
	}
	for _, message := range report.Errors {
		for _, rel := range g.goldenPaths() {
			if strings.Contains(message, rel) {
				fmt.Fprintf(&g.sb, "report-error %s\n", scrub.Replace(message))
				break
			}
		}
	}
}

func (g *familyGolden) recordFiles(root string) {
	g.t.Helper()
	for _, rel := range g.goldenPaths() {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		switch {
		case errors.Is(err, fs.ErrNotExist):
			fmt.Fprintf(&g.sb, "file %s absent\n", rel)
		case err != nil:
			g.t.Fatalf("read %s: %v", rel, err)
		case slices.Contains(quotedGoldenPaths, rel):
			fmt.Fprintf(&g.sb, "file %s %q\n", rel, data)
		default:
			sum := sha256.Sum256(data)
			fmt.Fprintf(&g.sb, "file %s sha256:%s\n", rel, hex.EncodeToString(sum[:]))
		}
	}
}

func goldenAdoptOptions(t *testing.T, root string) AdoptOptions {
	t.Helper()
	return AdoptOptions{Path: root, Profile: "framework", LockSourceRoot: newAdoptLockSource(t)}
}

// TestMarkdownFamilyAdoptionGolden replays enable, rerun, drift, forced repair, disable and
// re-enable on one repository.
func TestMarkdownFamilyAdoptionGolden(t *testing.T) {
	golden := &familyGolden{t: t}
	root := newTestRepo(t, "family-golden-lifecycle")
	opts := goldenAdoptOptions(t, root)
	golden.adopt("fresh enable", root, opts)
	golden.adopt("rerun", root, opts)
	mustWrite(t, filepath.Join(root, "tools", "markdownlint", "package.json"), "{}\n")
	golden.adopt("drift without force", root, opts)
	opts.Force = true
	golden.adopt("drift with force", root, opts)
	setDocumentationFacet(t, root, false)
	opts.Force = false
	golden.adopt("disable without force", root, opts)
	opts.Force = true
	golden.adopt("disable with force", root, opts)
	golden.adopt("disabled rerun", root, opts)
	setDocumentationFacet(t, root, true)
	golden.adopt("re-enable with force", root, opts)
	testsupport.AssertGolden(t, filepath.Join("testdata", "managed-family", "markdown-lifecycle.golden"), golden.sb.String())
}

// TestMarkdownFamilyForeignFilesGolden pins what first adoption does with files that already
// sit at the family's managed paths: preserved without --force, replaced with it. The
// Markdown family keeps refuse-on-first-adopt off.
func TestMarkdownFamilyForeignFilesGolden(t *testing.T) {
	golden := &familyGolden{t: t}
	for _, force := range []bool{false, true} {
		root := newTestRepo(t, fmt.Sprintf("family-golden-foreign-%t", force))
		mustWrite(t, filepath.Join(root, "tools", "markdownlint", "verify.mjs"), "// operator runner\n")
		mustWrite(t, filepath.Join(root, ".github", "workflows", "praetor-docs.yml"), "name: Operator\n")
		opts := goldenAdoptOptions(t, root)
		opts.Force = force
		golden.adopt(fmt.Sprintf("foreign first adoption force=%t", force), root, opts)
	}
	root := newTestRepo(t, "family-golden-dry-run")
	opts := goldenAdoptOptions(t, root)
	opts.DryRun = true
	golden.adopt("dry run", root, opts)
	testsupport.AssertGolden(t, filepath.Join("testdata", "managed-family", "markdown-foreign.golden"), golden.sb.String())
}

// TestMarkdownFamilyDisableRefusalGolden pins the refusal texts of a disable that meets a
// drifted asset or mixed line endings.
func TestMarkdownFamilyDisableRefusalGolden(t *testing.T) {
	golden := &familyGolden{t: t}
	for _, fixture := range []struct{ name, content string }{
		{"drifted", "operator edit\n"},
		{"mixed", "{\r\n}\n"},
	} {
		name, content := fixture.name, fixture.content
		root := newTestRepo(t, "family-golden-refusal-"+name)
		opts := goldenAdoptOptions(t, root)
		if _, err := Adopt(t.Context(), opts); err != nil {
			t.Fatal(err)
		}
		mustWrite(t, filepath.Join(root, "tools", "markdownlint", "package.json"), content)
		setDocumentationFacet(t, root, false)
		opts.Force = true
		golden.adopt("disable over "+name+" asset", root, opts)
	}
	testsupport.AssertGolden(t, filepath.Join("testdata", "managed-family", "markdown-refusal.golden"), golden.sb.String())
}

// TestManagedIgnoreFilesGolden pins the tail-block merge of .gitignore and .prettierignore
// through adoption: operator rules kept, a legacy unmarked rule migrated, CRLF preserved,
// and the formatter inventory shrinking on disable.
func TestManagedIgnoreFilesGolden(t *testing.T) {
	golden := &familyGolden{t: t}
	root := newTestRepo(t, "family-golden-ignores")
	mustWrite(t, filepath.Join(root, ".prettierrc"), "{}\n")
	mustWrite(t, filepath.Join(root, ".prettierignore"), "operator-output/\n")
	mustWrite(t, filepath.Join(root, ".gitignore"), "node_modules/\n/.workingdir/\n")
	opts := goldenAdoptOptions(t, root)
	golden.adopt("operator ignores", root, opts)
	setDocumentationFacet(t, root, false)
	opts.Force = true
	golden.adopt("operator ignores after disable", root, opts)
	crlf := newTestRepo(t, "family-golden-ignores-crlf")
	mustWrite(t, filepath.Join(crlf, ".gitignore"), "dist/\r\ncoverage/\r\n")
	golden.adopt("CRLF gitignore", crlf, goldenAdoptOptions(t, crlf))
	testsupport.AssertGolden(t, filepath.Join("testdata", "managed-family", "ignore-files.golden"), golden.sb.String())
}

// tailBlockGoldenInputs exercise every branch of the tail-block merge: empty, operator text,
// CRLF, mixed endings, an existing block, a legacy unmarked rule, duplicate, nested,
// unterminated, unmatched and indented markers, and the line bound on both sides.
func tailBlockGoldenInputs() []struct{ name, text string } {
	gitBlock, formatterBlock := ManagedGitIgnoreBlock(), ManagedFormatterIgnoreBlock(DocumentationFamilies())
	bound := strings.Repeat("x\n", 4095)
	return []struct{ name, text string }{
		{"empty", ""},
		{"operator text", "dist/\ncoverage/\n"},
		{"operator text without final newline", "dist/"},
		{"blank lines only", "\n\n\n"},
		{"CRLF", "dist/\r\ncoverage/\r\n"},
		{"mixed endings", "dist/\r\ncoverage/\n"},
		{"legacy unmarked rule", "node_modules/\n/.workingdir/\n/.workingdir2/\n"},
		{"git block alone", gitBlock},
		{"formatter block alone", formatterBlock},
		{"operator text above formatter block", "dist/\n\n" + formatterBlock},
		{"git block mid file", "a/\n" + gitBlock + "b/\n"},
		{"formatter block mid file", "a/\n" + formatterBlock + "b/\n"},
		{"git block twice", gitBlock + gitBlock},
		{"formatter block twice", formatterBlock + formatterBlock},
		{"git begin nested", "# BEGIN praetor private artifacts (praetorctl adopt)\n" + gitBlock},
		{"formatter begin nested", "# BEGIN praetor-managed artifacts (praetorctl adopt)\n" + formatterBlock},
		{"git unterminated", "a/\n# BEGIN praetor private artifacts (praetorctl adopt)\nb/\n"},
		{"formatter unterminated", "a/\n# BEGIN praetor-managed artifacts (praetorctl adopt)\nb/\n"},
		{"git unmatched end", "a/\n# END praetor private artifacts\n"},
		{"formatter unmatched end", "a/\n# END praetor-managed artifacts\n"},
		{"git indented marker", "a/\n  # BEGIN praetor private artifacts (praetorctl adopt)\n"},
		{"formatter indented marker", "a/\n# END praetor-managed artifacts \n"},
		{"at line bound", bound},
		{"above line bound", bound + "x\n"},
	}
}

// TestManagedTailBlockMergeGolden pins the tail-block merges of .gitignore and .prettierignore,
// and the formatter inventory verdict, for every input above.
func TestManagedTailBlockMergeGolden(t *testing.T) {
	var sb strings.Builder
	for _, input := range tailBlockGoldenInputs() {
		fmt.Fprintf(&sb, "== %s\n", input.name)
		merged, err := mergeGitIgnore(input.text)
		recordTailBlockResult(&sb, "gitignore", merged, err)
		for _, enabled := range []bool{true, false} {
			var families []managedasset.Family
			if enabled {
				families = DocumentationFamilies()
			}
			merged, err = mergeManagedIgnore(input.text, families)
			recordTailBlockResult(&sb, fmt.Sprintf("formatter documentation=%t", enabled), merged, err)
			recordTailBlockResult(&sb, fmt.Sprintf("verify formatter documentation=%t", enabled), "",
				VerifyManagedFormatterIgnore(input.text, families))
		}
	}
	testsupport.AssertGolden(t, filepath.Join("testdata", "managed-family", "tail-block.golden"), sb.String())
}

// recordTailBlockResult writes a merge result; outputs above 512 bytes are recorded as a
// digest so the bound inputs keep the golden readable.
func recordTailBlockResult(sb *strings.Builder, label, merged string, err error) {
	switch {
	case err != nil:
		fmt.Fprintf(sb, "%s error %s\n", label, err)
	case len(merged) > 512:
		sum := sha256.Sum256([]byte(merged))
		fmt.Fprintf(sb, "%s ok %d bytes sha256:%s\n", label, len(merged), hex.EncodeToString(sum[:]))
	default:
		fmt.Fprintf(sb, "%s ok %q\n", label, merged)
	}
}
