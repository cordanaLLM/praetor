package adopt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The governance step appends the declared verification targets to a Makefile that defines no
// verify-all and then merges the documentation gate block into the result
// (verificationMakefileReplacement). An edited block whose verify-all line is gone leaves the
// Makefile with no verify-all, so it is this append path, not the documentation step, that meets
// the edited block, and the forced re-adoption the refusal names has to restore it here too (#502).

// appendPathLockSource is the lock source the append-path sessions run with, so a remedy can be
// checked for naming it.
const appendPathLockSource = "/src/praetor"

// appendPathMakefile is an operator Makefile around a documentation gate block that lost its
// verify-all line: the edit that sends adoption down the append path.
func appendPathMakefile(t *testing.T) string {
	t.Helper()
	block := DocumentationMakefileBlock()
	edited := strings.Replace(block, "verify-all: docs-lint docs-figures\n", "", 1)
	if edited == block {
		t.Fatal("fixture edit did not apply")
	}
	return documentationMakefileAround(edited)
}

// appendPathSession is a documentation-enabled session over makefile with a declared verification
// plan and appendPathLockSource as the lock source.
func appendPathSession(t *testing.T, makefile string, opts AdoptOptions) *adoptSession {
	t.Helper()
	opts.LockSourceRoot = appendPathLockSource
	s := docsSession(t, map[string]string{makefileName: makefile}, opts)
	s.verification = &VerificationPlan{
		Status: verificationDeclared,
		Build:  [][]string{{"go", "build", "./..."}},
		Test:   [][]string{{"go", "test", "./..."}},
	}
	return s
}

// Positive: under --force the append path appends the verification targets, restores the edited
// block to the locked one, keeps every operator line, and records the write as a replace whose
// backup holds the edited Makefile.
func TestReconcileMakefile_Positive_ForceRestoresEditedBlockOnAppendPath(t *testing.T) {
	edited := appendPathMakefile(t)
	s := appendPathSession(t, edited, AdoptOptions{Force: true})
	if mayDefineVerificationTarget(edited) {
		t.Fatal("the fixture defines verify-all, so it does not take the append path")
	}
	if err := reconcileMakefile(t.Context(), s); err != nil {
		t.Fatalf("a forced run refused the edited block: %v", err)
	}
	got := mustRead(t, filepath.Join(s.repoPath, makefileName))
	for _, want := range []string{DocumentationMakefileBlock(), "build:\n\t@echo build\n", "test:\n\t@echo test\n",
		"verify-all:\n\t@$(PRAETORCTL) compile-context --verify\n"} {
		if !strings.Contains(got, want) {
			t.Fatalf("Makefile lacks %q:\n%s", want, got)
		}
	}
	entry := replacedEntry(t, s.report, makefileName)
	if !strings.HasPrefix(entry.Details, verificationMakefileRestored) || !strings.Contains(entry.Details, "backup: ") {
		t.Fatalf("replace detail = %q", entry.Details)
	}
	if backup := mustRead(t, backupFile(s, makefileName)); backup != edited {
		t.Fatalf("backup = %q, want the edited Makefile", backup)
	}
}

// Negative: without --force the edited block stops the run, the Makefile stays as it was, nothing is
// backed up, and the refusal names the forced re-adoption with this run's lock source, the run the
// positive case shows restoring the block.
func TestReconcileMakefile_Negative_EditedBlockOnAppendPathNeedsForce(t *testing.T) {
	edited := appendPathMakefile(t)
	s := appendPathSession(t, edited, AdoptOptions{})
	err := reconcileMakefile(t.Context(), s)
	want := errDocumentationBlockEdited.Error() + "; review it and rerun " + ForceCommand(appendPathLockSource)
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v, want %q", err, want)
	}
	if got := mustRead(t, filepath.Join(s.repoPath, makefileName)); got != edited {
		t.Fatalf("a refused run changed the Makefile:\n%s", got)
	}
	if fileExists(backupFile(s, makefileName)) || len(s.report.Replaced()) != 0 {
		t.Fatalf("a refused run backed up or replaced the Makefile: %+v", s.report.ActionDetails)
	}
}

// Boundary: a forced dry run plans the same replace and writes neither the Makefile nor a backup;
// a forced run over a Makefile with no block at all is a plain append, not a replace.
func TestReconcileMakefile_Boundary_ForcedDryRunPlansAndPlainAppendIsNoReplace(t *testing.T) {
	edited := appendPathMakefile(t)
	dry := appendPathSession(t, edited, AdoptOptions{Force: true, DryRun: true})
	if err := reconcileMakefile(t.Context(), dry); err != nil {
		t.Fatal(err)
	}
	if entry := replacedEntry(t, dry.report, makefileName); !strings.HasPrefix(entry.Details, verificationMakefileRestored) {
		t.Fatalf("planned replace detail = %q", entry.Details)
	}
	if mustRead(t, filepath.Join(dry.repoPath, makefileName)) != edited {
		t.Fatal("a dry run wrote the Makefile")
	}
	if _, err := os.Lstat(filepath.Join(dry.repoPath, workingDirPath)); !os.IsNotExist(err) {
		t.Fatalf("a dry run created the backup root (lstat err=%v)", err)
	}
	plain := appendPathSession(t, "build:\n\t@echo build\n", AdoptOptions{Force: true})
	if err := reconcileMakefile(t.Context(), plain); err != nil {
		t.Fatal(err)
	}
	if action, ok := actionOf(plain.report, makefileName); !ok || action.Action != actionAppend || len(plain.report.Replaced()) != 0 {
		t.Fatalf("a block-free Makefile was not a plain append: %+v", plain.report.ActionDetails)
	}
	if fileExists(backupFile(plain, makefileName)) {
		t.Fatal("a plain append took a backup")
	}
}

// documentationMakefileBlockEdited names exactly the block only --force restores: an edited block
// (positive); the current block, an earlier Praetor block and no block at all are not edited
// (negative); a lone marker counts as edited, and mixed line endings are an error (boundary).
func TestDocumentationMakefileBlockEdited(t *testing.T) {
	cases := map[string]struct {
		makefile string
		edited   bool
		err      bool
	}{
		"positive: edited block":  {makefile: documentationMakefileAround(strings.Replace(DocumentationMakefileBlock(), "docs-lint:\n", "docs-lint: extra\n", 1)), edited: true},
		"negative: current block": {makefile: documentationMakefileAround(DocumentationMakefileBlock())},
		"negative: earlier block": {makefile: documentationMakefileAround(priorDocumentationMakefileBlocks[0])},
		"negative: no block":      {makefile: "build:\n\t@echo build\n"},
		"boundary: lone marker":   {makefile: "build:\n" + documentationMakefileBegin + "\n", edited: true},
		"boundary: mixed endings": {makefile: "build:\r\n\t@echo build\n", err: true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			edited, err := documentationMakefileBlockEdited(tc.makefile)
			if (err != nil) != tc.err || edited != tc.edited {
				t.Fatalf("edited = %t, err = %v; want %t, error %t", edited, err, tc.edited, tc.err)
			}
		})
	}
}
