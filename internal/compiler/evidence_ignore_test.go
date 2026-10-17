package compiler

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/testsupport"
	"github.com/cordanaLLM/praetor/internal/util"
)

// gatedProse fails the context caveman lint on article density: an adopter's own paragraph
// before its caveman rewrite.
const gatedProse = "\nSearch for an existing implementation before adding one. Grep the repository for the " +
	"capability and extend the code that is already there. Two implementations of one behavior are a " +
	"defect: they drift, and the second one stops matching the first.\n"

// evidenceRepo is a Git work tree holding .gitignore with rules, or no .gitignore when rules
// is empty. No .workingdir/ directory exists in it.
func evidenceRepo(t *testing.T, rules string) string {
	t.Helper()
	root := t.TempDir()
	testsupport.InitGitRepoWithOrigin(t, root, "")
	if rules != "" {
		writeCanonicalFixture(t, filepath.Join(root, ".gitignore"), rules)
	}
	return root
}

// Positive: a rule excluding the directory holds before the directory exists, whatever its
// spelling; Git cannot re-include anything under an excluded directory, so a later negation of
// the evidence directory does not undo it.
func TestEvidenceIgnored_Positive(t *testing.T) {
	for name, rules := range map[string]string{
		"anchored-directory":        "/.workingdir/\n",
		"bare-name":                 ".workingdir\n",
		"negation-under-excluded":   "/.workingdir/\n!.workingdir/evidence/\n",
		"evidence-directory-itself": "/.workingdir/evidence/\n",
	} {
		t.Run(name, func(t *testing.T) {
			root := evidenceRepo(t, rules)
			ignored, err := EvidenceIgnored(t.Context(), root, config.EvidenceDirDefault)
			if err != nil || !ignored {
				t.Fatalf("ignored = %v, err = %v; want ignored", ignored, err)
			}
			if err := CheckEvidenceIgnored(t.Context(), root, config.EvidenceDirDefault); err != nil {
				t.Fatalf("check: %v", err)
			}
		})
	}
}

// Negative: without a rule, and outside a work tree for the bare probe, the answer is no or an
// error, and the check names the directory and both remedies.
func TestEvidenceIgnored_Negative(t *testing.T) {
	root := evidenceRepo(t, "user-output/\n")
	ignored, err := EvidenceIgnored(t.Context(), root, config.EvidenceDirDefault)
	if err != nil || ignored {
		t.Fatalf("ignored = %v, err = %v; want not ignored", ignored, err)
	}
	err = CheckEvidenceIgnored(t.Context(), root, config.EvidenceDirDefault)
	if !errors.Is(err, ErrEvidenceNotIgnored) {
		t.Fatalf("want ErrEvidenceNotIgnored, got %v", err)
	}
	for _, want := range []string{config.EvidenceDirDefault, "praetorctl compile-context", "adoption.decline", "/.workingdir/"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("check error lacks %q: %v", want, err)
		}
	}
	outside := t.TempDir()
	if present, presentErr := util.GitWorktreePresent(t.Context(), outside); presentErr != nil || present {
		t.Skipf("temporary directory sits inside a Git work tree (%v, %v)", present, presentErr)
	}
	if _, err := EvidenceIgnored(t.Context(), outside, config.EvidenceDirDefault); err == nil {
		t.Fatal("the bare probe outside a work tree must fail, not answer")
	}
}

// Boundary: a negation that takes effect, such as .workingdir/* then !.workingdir/evidence/,
// reads as not ignored, as does an existing evidence directory; a directory in no work tree
// passes the check, since no commit can publish it.
func TestEvidenceIgnored_Boundary(t *testing.T) {
	root := evidenceRepo(t, ".workingdir/*\n!.workingdir/evidence/\n")
	if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(config.EvidenceDirDefault)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := CheckEvidenceIgnored(t.Context(), root, config.EvidenceDirDefault); !errors.Is(err, ErrEvidenceNotIgnored) {
		t.Fatalf("an effective re-include must fail the check, got %v", err)
	}
	outside := t.TempDir()
	if present, err := util.GitWorktreePresent(t.Context(), outside); err != nil || present {
		t.Skipf("temporary directory sits inside a Git work tree (%v, %v)", present, err)
	}
	if err := CheckEvidenceIgnored(t.Context(), outside, config.EvidenceDirDefault); err != nil {
		t.Fatalf("outside a work tree the check must pass: %v", err)
	}
}

// LintContextText is LintContext over text: terse text passes with its counts, prose fails
// naming the label, and prose inside the rendered register block is left to its renderer.
func TestLintContextText(t *testing.T) {
	lint, err := LintContextText("AGENTS.md", fixtureSource)
	if err != nil || !lint.Report.Passed() {
		t.Fatalf("terse text: %v", err)
	}
	_, err = LintContextText("merged/AGENTS.md", fixtureSource+gatedProse)
	if !errors.Is(err, ErrContextProse) || !strings.Contains(err.Error(), "merged/AGENTS.md") {
		t.Fatalf("prose must fail naming the label, got %v", err)
	}
	masked := fixtureSource + config.RegisterBlockStart + gatedProse + config.RegisterBlockEnd + "\n"
	lint, err = LintContextText("AGENTS.md", masked)
	if err != nil || lint.MaskedLines == 0 {
		t.Fatalf("register block prose must be masked: lines=%d err=%v", lint.MaskedLines, err)
	}
}

// Negative: the write lints after writing. A prose AGENTS.md still gets every target, and the run
// fails with the lint rather than reporting success.
func TestCompileContextProjections_Negative_LintsTheWrittenSource(t *testing.T) {
	root := skillFixture(t)
	writeCanonicalFixture(t, filepath.Join(root, "AGENTS.md"), fixtureSource+gatedProse)
	var out strings.Builder
	err := CompileContextProjections(t.Context(), &out, NewTranspiler(), filepath.Join(root, "AGENTS.md"), root)
	if !errors.Is(err, ErrContextProse) || !strings.Contains(err.Error(), "context written, but compile-context --verify will fail") {
		t.Fatalf("want the lint failure after the write, got %v", err)
	}
	if strings.Contains(out.String(), "completed successfully") {
		t.Fatalf("a failed lint printed success:\n%s", out.String())
	}
	if !strings.Contains(readFixtureText(t, filepath.Join(root, "CLAUDE.md")), "Two implementations") {
		t.Fatal("the targets must still be written")
	}
}

// Boundary: a persona or skill that fails the gate fails the write the same way, with the file
// named, while AGENTS.md itself passes.
func TestCompileContextProjections_Boundary_LintsCanonicalSkills(t *testing.T) {
	root := skillFixture(t)
	skill := filepath.Join(root, filepath.FromSlash(CanonicalSkillsRel), "repo-adopt", SkillEntryName)
	writeCanonicalFixture(t, skill, readFixtureText(t, skill)+gatedProse)
	err := compileFixture(t, root)
	if !errors.Is(err, ErrAgentTextProse) || !strings.Contains(err.Error(), filepath.Join(CanonicalSkillsRel, "repo-adopt")) {
		t.Fatalf("want the skill lint failure, got %v", err)
	}
}

// Negative: verify runs every check and returns every failure. A source with no register block,
// prose below it and an unignored evidence directory fails all three in one run.
func TestVerifyCompiledContext_Negative_ReportsEveryFailure(t *testing.T) {
	root := evidenceRepo(t, "")
	source := filepath.Join(root, "AGENTS.md")
	writeCanonicalFixture(t, source, fixtureSource+gatedProse)
	tr := NewTranspiler()
	res, err := tr.Compile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := tr.WriteOutputs(res, root); err != nil {
		t.Fatal(err)
	}
	err = VerifyCompiledContext(t.Context(), io.Discard, tr, source, root)
	for _, want := range []error{ErrRegisterBlockOutOfSync, ErrContextProse, ErrEvidenceNotIgnored} {
		if !errors.Is(err, want) {
			t.Errorf("verify error lacks %v: %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "out of sync with canonical") {
		t.Errorf("targets compiled from the source must verify: %v", err)
	}
}

// evidenceDirRepo is evidenceRepo with AGENTS.md and a manifest whose register policy sends
// evidence to evidenceDir.
func evidenceDirRepo(t *testing.T, rules, evidenceDir string) string {
	t.Helper()
	root := evidenceRepo(t, rules)
	writeCanonicalFixture(t, filepath.Join(root, "AGENTS.md"), fixtureSource)
	writeCanonicalFixture(t, filepath.Join(root, ".standards.yaml"),
		"version: 1\nregister:\n  evidence:\n    dir: "+evidenceDir+"\n")
	return root
}

// verifyEvidenceDir runs compile-context --verify over the repository at root.
func verifyEvidenceDir(t *testing.T, root string) error {
	t.Helper()
	return VerifyCompiledContext(t.Context(), io.Discard, NewTranspiler(), filepath.Join(root, "AGENTS.md"), root)
}

// Negative: verify probes the directory the manifest configures, not the default. A repository
// that ignores .workingdir/ but sends evidence to scratch/evidence/ fails, naming that
// directory and the root an operator rule has to cover.
func TestVerifyCompiledContext_Negative_ProbesConfiguredEvidenceDir(t *testing.T) {
	root := evidenceDirRepo(t, "/.workingdir/\n", "scratch/evidence/")
	err := verifyEvidenceDir(t, root)
	if !errors.Is(err, ErrEvidenceNotIgnored) {
		t.Fatalf("an unignored configured directory must fail verify, got %v", err)
	}
	for _, want := range []string{"git does not ignore scratch/evidence/ in ", "add /scratch/ to the operator-owned .gitignore"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("verify error lacks %q: %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "git does not ignore "+config.EvidenceDirDefault) {
		t.Errorf("verify probed the default directory: %v", err)
	}
}

// Positive: a repository that keeps only its legacy scratch root private, as .workingdir2/**,
// and sends evidence there passes the ignore check although .workingdir/ is not ignored.
func TestVerifyCompiledContext_Positive_ConfiguredEvidenceDirIgnored(t *testing.T) {
	root := evidenceDirRepo(t, ".workingdir2/**\n", ".workingdir2/evidence/")
	if err := verifyEvidenceDir(t, root); errors.Is(err, ErrEvidenceNotIgnored) {
		t.Fatalf("an ignored configured directory must pass the ignore check: %v", err)
	}
	if err := CheckEvidenceIgnored(t.Context(), root, ".workingdir2/evidence"); err != nil {
		t.Fatalf("check without the trailing slash: %v", err)
	}
}

// Boundary: a directory config.CheckEvidenceDir refuses is an error before any probe, inside or
// outside a work tree, never a pass and never ErrEvidenceNotIgnored.
func TestCheckEvidenceIgnored_Boundary_RefusesInvalidDir(t *testing.T) {
	root := evidenceRepo(t, "/.workingdir/\n")
	for _, dir := range []string{"", "/abs/evidence/", "../evidence/", ".git/evidence/"} {
		if _, err := EvidenceIgnored(t.Context(), root, dir); err == nil || !strings.Contains(err.Error(), "register evidence dir") {
			t.Errorf("EvidenceIgnored(%q) = %v; want the directory refused", dir, err)
		}
		err := CheckEvidenceIgnored(t.Context(), t.TempDir(), dir)
		if err == nil || errors.Is(err, ErrEvidenceNotIgnored) {
			t.Errorf("CheckEvidenceIgnored(%q) = %v; want the directory refused", dir, err)
		}
	}
}
