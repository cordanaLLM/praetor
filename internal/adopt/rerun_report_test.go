package adopt

import (
	"path/filepath"
	"strings"
	"testing"
)

// A re-adoption lists an existing persona copy and an existing praetor pre-commit hook as
// reconciled, never as created, and a hand-edited persona copy as replaced with a backup: the
// copies follow the vendor context files' record (recordProjections).

// personaCopy is one vendor copy of a generated canonical persona.
const personaCopy = ".claude/agents/repo-auditor.md"

// fallbackHook is the pre-commit hook adoption installs when lefthook is not available, as the
// report names it.
const fallbackHook = ".git/hooks/pre-commit"

// Positive: a forced re-run over an unchanged adoption lists no file as created, persona copies,
// the pre-commit hook and the Paperclip harness included; each is reconciled.
func TestAdopt_Positive_ForcedRerunListsNoExistingCopyOrHookAsCreated(t *testing.T) {
	repoPath, source := adoptedRepo(t, "rerun-created")
	for _, rel := range []string{personaCopy, fallbackHook, paperclipFile} {
		if !fileExists(filepath.Join(repoPath, filepath.FromSlash(rel))) {
			t.Fatalf("fixture lacks %s", rel)
		}
	}
	rep := readoptWith(t, repoPath, source, AdoptOptions{Force: true})
	if len(rep.CreatedFiles) != 0 {
		t.Errorf("existing files listed as created: %v", rep.CreatedFiles)
	}
	for _, rel := range []string{personaCopy, fallbackHook, paperclipFile} {
		if !hasAction(rep, rel, actionReconcile) {
			t.Errorf("%s not reconciled: %+v", rel, rep.ActionDetails)
		}
	}
	if len(rep.Replaced()) != 0 {
		t.Fatalf("unchanged files replaced: %+v", rep.Replaced())
	}
}

// Negative: a hand edit of a persona copy is dropped by a plain re-run, since compile-context
// --verify demands the copy's bytes, and reported as a replace whose backup keeps the edit; a
// hand-edited praetor hook is refreshed and reconciled, not created.
func TestAdopt_Negative_HandEditedPersonaCopyReplacedWithBackup(t *testing.T) {
	repoPath, source := adoptedRepo(t, "rerun-persona-edit")
	full := filepath.Join(repoPath, filepath.FromSlash(personaCopy))
	canonical := mustRead(t, full)
	edited := canonical + "Local persona note.\n"
	mustWrite(t, full, edited)
	hook := filepath.Join(repoPath, filepath.FromSlash(fallbackHook))
	mustWrite(t, hook, mustRead(t, hook)+"# local line\n")
	rep := readoptWith(t, repoPath, source, AdoptOptions{})
	entry := replacedEntry(t, rep, personaCopy)
	if !strings.Contains(entry.Details, `removed "Local persona note."`) {
		t.Fatalf("replace detail %q", entry.Details)
	}
	if got := mustRead(t, backupNamedIn(t, repoPath, entry.Details)); got != edited {
		t.Fatalf("backup = %q, want the hand-edited copy", got)
	}
	if mustRead(t, full) != canonical {
		t.Fatal("persona copy not restored to the canonical text")
	}
	if contains(rep.CreatedFiles, fallbackHook) || !hasAction(rep, fallbackHook, actionReconcile) ||
		strings.Contains(mustRead(t, hook), "# local line") {
		t.Fatalf("edited praetor hook not refreshed as reconciled: %+v", rep.ActionDetails)
	}
}

// Boundary: without --force, a symlinked backup root fails adoption before its first write when
// a persona copy holds a hand edit the agent-definitions step would back up; with every copy
// unedited the same root does not stop a plain run
// (TestAdopt_Boundary_PlainRunAcceptsSymlinkedBackupRootWithoutVendorEdit).
func TestAdopt_Boundary_PlainRunRefusesSymlinkedBackupRootForPersonaCopyEdit(t *testing.T) {
	repoPath, source := adoptedRepo(t, "rerun-persona-backup-root")
	full := filepath.Join(repoPath, filepath.FromSlash(personaCopy))
	mustWrite(t, full, mustRead(t, full)+"Local persona note.\n")
	shared := plantBackupRootLink(t, repoPath)
	before := snapshotTree(t, repoPath)
	_, err := Adopt(t.Context(), AdoptOptions{LockSourceRoot: source, Path: repoPath})
	if err == nil || !strings.Contains(err.Error(), "agent-definitions preflight") || !strings.Contains(err.Error(), adoptBackupRoot) {
		t.Fatalf("err = %v, want an agent-definitions preflight refusal naming the backup root", err)
	}
	assertTreeUnchanged(t, before, snapshotTree(t, repoPath))
	assertDirEmpty(t, shared)
}
