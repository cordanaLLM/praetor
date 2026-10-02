// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"errors"
	"flag"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/docsref"
	"github.com/cordanaLLM/praetor/internal/testsupport"
)

// declaredFunctions returns the plain functions this package's non-test sources declare.
func declaredFunctions(t *testing.T) map[string]bool {
	t.Helper()
	sources, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob sources: %v", err)
	}
	declared := map[string]bool{}
	fset := token.NewFileSet()
	for _, source := range sources {
		if strings.HasSuffix(source, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, source, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse %s: %v", source, err)
		}
		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil {
				declared[fn.Name.Name] = true
			}
		}
	}
	return declared
}

// TestCommandHandlerNames_Positive_EveryCommandNamesADeclaredFunction pins the one assumption
// the docs references gate makes about the dispatch table: each handler is a named top-level
// function, so the gate can read its code. A closure registered as a handler fails here
// before it makes the gate fail.
func TestCommandHandlerNames_Positive_EveryCommandNamesADeclaredFunction(t *testing.T) {
	names, err := commandHandlerNames()
	if err != nil {
		t.Fatalf("commandHandlerNames: %v", err)
	}
	if len(names) != len(commandTable()) {
		t.Fatalf("named %d commands, the table has %d", len(names), len(commandTable()))
	}
	declared := declaredFunctions(t)
	for command, handler := range names {
		if !declared[handler] {
			t.Errorf("command %s is handled by %q, which this package does not declare as a function", command, handler)
		}
	}
	if names["conform"] != "runAdopt" || names["docs"] != "runDocs" {
		t.Errorf("aliases must name their shared handler: conform=%q docs=%q", names["conform"], names["docs"])
	}
}

func TestRunDocsReferences_Negative_RequiresAPraetorCheckout(t *testing.T) {
	err := dispatchCommand("docs", []string{"references", "--path=" + t.TempDir()})
	mustErrContain(t, err, "checks a Praetor source checkout")
	err = dispatchCommand("docs", []string{"references", "extra"})
	mustErrContain(t, err, "accepts no positional arguments")
}

func TestRunDocsReferences_Boundary_HelpAndUsage(t *testing.T) {
	if err := dispatchCommand("docs", []string{"references", "-h"}); !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("docs references -h must be a satisfied help request, got %v", err)
	}
	out, err := captureStdout(t, func() error { return dispatchCommand("docs", []string{"help"}) })
	if err != nil {
		t.Fatalf("docs help: %v", err)
	}
	mustContain(t, out, "references [--path=.]")
	// A checkout directory that is a file, not a directory, is refused like a missing one.
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "cmd"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "cmd", "standardsctl"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	err = dispatchCommand("docs", []string{"references", "--path=" + root})
	mustErrContain(t, err, "checks a Praetor source checkout")
}

// adopterSurfaces is an adopter's manifest: a build script documented in its onboarding
// guide and a release workflow declared without documentation.
const adopterSurfaces = `version: 1
docs_surfaces:
  - name: "build script"
    paths:
      - "scripts/build.sh"
    docs:
      - "docs/onboarding.md"
  - name: "release workflow"
    paths:
      - ".github/workflows/release.yml"
`

// driftFixture is an adopter repository (no cmd/standardsctl) with one base commit.
type driftFixture struct {
	dir  string
	env  []string
	base string
}

// newDriftFixture commits the adopter's manifest, script, guide and workflow on main.
func newDriftFixture(t *testing.T) driftFixture {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not available: %v", err)
	}
	f := driftFixture{dir: t.TempDir(), env: testsupport.HermeticGitEnv(t)}
	writeFixtureFile(t, f.dir, ".standards.yaml", adopterSurfaces)
	writeFixtureFile(t, f.dir, "scripts/build.sh", "#!/bin/sh\necho build\n")
	writeFixtureFile(t, f.dir, "docs/onboarding.md", "# Onboarding\n")
	writeFixtureFile(t, f.dir, ".github/workflows/release.yml", "on: push\n")
	f.git(t, "init", "-q", "-b", "main")
	f.git(t, "add", "-A")
	f.git(t, "commit", "-q", "-m", "base")
	f.base = strings.TrimSpace(f.git(t, "rev-parse", "HEAD"))
	return f
}

// git runs one fixture git command and fails the test when it fails.
func (f driftFixture) git(t *testing.T, args ...string) string {
	t.Helper()
	out, err := runFixtureGit(t, f.dir, f.env, args...)
	if err != nil {
		t.Fatalf("git %v: %v (%s)", args, err, out)
	}
	return out
}

// commit rewrites each file and commits them with message.
func (f driftFixture) commit(t *testing.T, message string, files ...string) {
	t.Helper()
	for _, rel := range files {
		writeFixtureFile(t, f.dir, rel, "changed "+message+"\n")
	}
	f.git(t, "add", "-A")
	f.git(t, "commit", "-q", "-m", message)
}

// references runs docs references on the fixture and returns its standard output, standard
// error and error.
func (f driftFixture) references(t *testing.T, extra ...string) (string, string, error) {
	t.Helper()
	var stderr string
	stdout, err := captureStdout(t, func() error {
		out, runErr := captureStderr(t, func() error {
			return dispatchCommand("docs", append([]string{"references", "--path=" + f.dir}, extra...))
		})
		stderr = out
		return runErr
	})
	return stdout, stderr, err
}

// Positive (#608): an adopter checkout is checked, not refused. A run without --base checks
// the declaration alone; a documentation-only change passes; a change to the build script
// that edits its guide passes and names the guide.
func TestRunDocsReferences_Positive_AdopterSurfaceWithItsDocumentation(t *testing.T) {
	f := newDriftFixture(t)
	stdout, _, err := f.references(t)
	if err != nil {
		t.Fatalf("declaration check: %v\n%s", err, stdout)
	}
	mustContain(t, stdout, "skipped command, flag and path references: not a Praetor source checkout",
		"=== Documentation Surfaces: 2 declared in .standards.yaml ===", "change not checked")
	f.commit(t, "docs: explain the onboarding step", "docs/onboarding.md")
	stdout, _, err = f.references(t, "--base="+f.base)
	if err != nil {
		t.Fatalf("documentation-only change: %v\n%s", err, stdout)
	}
	mustContain(t, stdout, "Surfaces changed:   0 of 2", "[PASS] every changed surface")
	f.commit(t, "fix: refuse the production build", "scripts/build.sh", "docs/onboarding.md")
	stdout, _, err = f.references(t, "--base="+f.base, "--head=HEAD")
	if err != nil {
		t.Fatalf("documented change: %v\n%s", err, stdout)
	}
	mustContain(t, stdout, "documented build script: docs/onboarding.md", "[PASS] every changed surface")
}

// Negative (#608): a change to the script and the workflow alone fails, naming the surface, its
// path and the guide mapped to it, and reporting the workflow as unmapped rather than passing.
// A declared guide that does not exist, a waiver flag without --base and a checkout that is
// neither Praetor's nor declares a surface are refused.
func TestRunDocsReferences_Negative_AdopterSurfaceWithoutItsDocumentation(t *testing.T) {
	f := newDriftFixture(t)
	f.commit(t, "fix: refuse the production build", "scripts/build.sh", ".github/workflows/release.yml")
	_, stderr, err := f.references(t, "--base="+f.base)
	mustErrContain(t, err, "[FAIL] 2 changed surface(s) without their documentation")
	mustContain(t, stderr, "build script: scripts/build.sh changed without an edit to docs/onboarding.md",
		"release workflow: .github/workflows/release.yml changed, and the surface is unmapped")
	writeFixtureFile(t, f.dir, ".standards.yaml", strings.Replace(adopterSurfaces, "docs/onboarding.md", "docs/missing.md", 1))
	_, stderr, err = f.references(t)
	mustErrContain(t, err, "[FAIL] 1 docs_surfaces glob(s) in .standards.yaml select nothing")
	mustContain(t, stderr, `build script: docs glob "docs/missing.md" matches no document of the repository`)
	err = dispatchCommand("docs", []string{"references", "--path=" + f.dir, "--pr-body-file=body.txt"})
	mustErrContain(t, err, "--head and --pr-body-file need --base")
	writeFixtureFile(t, f.dir, ".standards.yaml", "version: 1\n")
	err = dispatchCommand("docs", []string{"references", "--path=" + f.dir})
	mustErrContain(t, err, "has no cmd/standardsctl directory and no docs_surfaces in .standards.yaml")
	err = dispatchCommand("docs", []string{"references", "--path=" + f.dir, "--base=" + f.base})
	mustErrContain(t, err, "has no cmd/standardsctl directory and no docs_surfaces in .standards.yaml")
}

// Boundary (#608): a Docs-Waiver: trailer without a reason admits nothing; the pull request
// body's "no docs needed: <reason>" admits the change and is printed with the waived finding;
// a trailer with a reason does the same for the unmapped workflow. An unreadable body file and
// a revision shaped like an option are refused.
func TestRunDocsReferences_Boundary_AdopterWaivers(t *testing.T) {
	f := newDriftFixture(t)
	f.commit(t, "fix: refuse the production build\n\nDocs-Waiver:\n", "scripts/build.sh")
	_, _, err := f.references(t, "--base="+f.base)
	mustErrContain(t, err, "[FAIL] 1 changed surface(s)")
	body := writeFixtureFile(t, t.TempDir(), "body.txt", "## Summary\n\nno docs needed: the dry run is unchanged\n")
	stdout, _, err := f.references(t, "--base="+f.base, "--pr-body-file="+body)
	if err != nil {
		t.Fatalf("pull request body waiver: %v\n%s", err, stdout)
	}
	mustContain(t, stdout, "waiver pull request body: the dry run is unchanged",
		"waived build script: scripts/build.sh changed without an edit to docs/onboarding.md")
	f.commit(t, "ci: publish on tags\n\nDocs-Waiver: the documented release flow is unchanged\n", ".github/workflows/release.yml")
	stdout, _, err = f.references(t, "--base="+f.base)
	if err != nil {
		t.Fatalf("commit trailer waiver: %v\n%s", err, stdout)
	}
	mustContain(t, stdout, ": the documented release flow is unchanged", "Surfaces changed:   2 of 2",
		"waived release workflow: .github/workflows/release.yml changed, and the surface is unmapped")
	_, _, err = f.references(t, "--base="+f.base, "--pr-body-file="+filepath.Join(f.dir, "absent.txt"))
	mustErrContain(t, err, "read the pull request body")
	_, _, err = f.references(t, "--base=--upload-pack=x")
	mustErrContain(t, err, "invalid git revision")
}

// recordFlag is one flagged path of an Accepted decision record.
func recordFlag(line int) docsref.Finding {
	return docsref.Finding{Doc: "docs/adr/0001-a.md", Line: line, Message: `path "internal/retired" is not in the repository`}
}

// A flag on an Accepted decision record is printed with its remedy and never fails the check
// (#353, the part moved from #355).
func TestPrintDocsReferences_Positive_RecordFlagsDoNotFail(t *testing.T) {
	report := &docsref.Report{Documents: 2, Records: 1, Flagged: []docsref.Finding{recordFlag(7)}}
	out, err := captureStdout(t, func() error { return printDocsReferences(".", report) })
	if err != nil {
		t.Fatalf("a flag failed the check: %v", err)
	}
	mustContain(t, out, "Decision records:   1 (Accepted; repository paths only)",
		"[FLAG] 1 reference(s) in Accepted decision records do not resolve",
		`docs/adr/0001-a.md:7: path "internal/retired" is not in the repository`,
		"a record that supersedes it states what changed", "[PASS]")
}

// A finding still fails the check when flags are present beside it.
func TestPrintDocsReferences_Negative_FindingFailsBesideFlags(t *testing.T) {
	report := &docsref.Report{Records: 1, Flagged: []docsref.Finding{recordFlag(7)},
		Findings: []docsref.Finding{{Doc: "docs/guide.md", Line: 3, Message: `path "x/y" is not in the repository`}}}
	out, err := captureStdout(t, func() error { return printDocsReferences(".", report) })
	mustErrContain(t, err, "1 documentation reference(s) do not resolve")
	mustContain(t, out, "[FLAG] 1 reference(s)")
}

// Boundary: no flag prints no flag section, and more flags than the print bound list only
// the bound while the count states them all.
func TestPrintDocsReferences_Boundary_FlagSectionBounds(t *testing.T) {
	out, err := captureStdout(t, func() error { return printDocsReferences(".", &docsref.Report{}) })
	if err != nil || strings.Contains(out, "[FLAG]") {
		t.Fatalf("an empty report printed a flag section: %v\n%s", err, out)
	}
	flagged := make([]docsref.Finding, 0, maxReportedFindings+1)
	for line := 1; line <= maxReportedFindings+1; line++ {
		flagged = append(flagged, recordFlag(line))
	}
	out, err = captureStdout(t, func() error { return printDocsReferences(".", &docsref.Report{Flagged: flagged}) })
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "[FLAG] 513 reference(s)", "docs/adr/0001-a.md:512:")
	if strings.Contains(out, "docs/adr/0001-a.md:513:") {
		t.Fatal("printed a flag past the print bound")
	}
}
