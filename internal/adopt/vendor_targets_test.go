package adopt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/compiler"
)

const vendorContextFile = "CLAUDE.md"

// adoptedRepo runs a first plain adoption of a new repository and returns its path and the
// catalog source it was adopted from.
func adoptedRepo(t *testing.T, name string) (repoPath, source string) {
	t.Helper()
	repoPath, source = newTestRepo(t, name), newAdoptLockSource(t)
	if _, err := Adopt(t.Context(), AdoptOptions{LockSourceRoot: source, Path: repoPath}); err != nil {
		t.Fatalf("first adoption: %v", err)
	}
	return repoPath, source
}

// readoptWith runs adoption again over repoPath with opts.
func readoptWith(t *testing.T, repoPath, source string, opts AdoptOptions) *AdoptReport {
	t.Helper()
	opts.LockSourceRoot, opts.Path = source, repoPath
	rep, err := Adopt(t.Context(), opts)
	if err != nil {
		t.Fatalf("re-adoption %+v: %v", opts, err)
	}
	assertNoIssues(t, rep)
	return rep
}

// backupNamedIn returns the backup path a replace detail names, below repoPath.
func backupNamedIn(t *testing.T, repoPath, detail string) string {
	t.Helper()
	_, rel, ok := strings.Cut(detail, "; backup: ")
	if !ok || !strings.HasPrefix(rel, adoptBackupRoot+"/") {
		t.Fatalf("detail %q names no backup under %s", detail, adoptBackupRoot)
	}
	return filepath.Join(repoPath, filepath.FromSlash(rel))
}

// Positive: a hand-edited CLAUDE.md is replaced on a plain run, since audit demands its exact
// bytes, and the edit is kept under the backup root; the dry run before it plans that replace
// and writes neither the file nor a backup. The other vendor files are only synchronized.
func TestAdopt_Positive_HandEditedVendorFileOnPlainRunReplacedWithBackup(t *testing.T) {
	repoPath, source := adoptedRepo(t, "vendor-edit")
	full := filepath.Join(repoPath, vendorContextFile)
	projection := mustRead(t, full)
	edited := projection + "Local note an operator added by hand.\n"
	mustWrite(t, full, edited)

	preview := readoptWith(t, repoPath, source, AdoptOptions{DryRun: true})
	planned := replacedEntry(t, preview, vendorContextFile)
	if mustRead(t, full) != edited || fileExists(backupNamedIn(t, repoPath, planned.Details)) {
		t.Fatal("dry run wrote the vendor file or its backup")
	}

	rep := readoptWith(t, repoPath, source, AdoptOptions{})
	entry := replacedEntry(t, rep, vendorContextFile)
	if !strings.Contains(entry.Details, `(-1/+0 lines, removed "Local note an operator added by hand.")`) {
		t.Fatalf("replace detail %q", entry.Details)
	}
	if got := mustRead(t, backupNamedIn(t, repoPath, entry.Details)); got != edited {
		t.Fatalf("backup = %q, want the hand-edited file", got)
	}
	if mustRead(t, full) != projection || len(rep.Replaced()) != 1 {
		t.Fatalf("vendor file not restored, or other files replaced: %+v", rep.Replaced())
	}
}

// lockedPaths are the audit-locked files a re-adoption rewrites whose existing copy must never
// be listed as created or, when unchanged, as replaced.
var lockedPaths = []string{lockFile, devcontainerFile, vendorContextFile, ".cursor/rules/hiss-invariants.mdc", makefileName,
	markdownlintConfig, ".config/archetypes/template-seed.yaml"}

// Negative: a plain and a forced re-adoption over an unchanged adopted repository replace
// nothing, and no existing locked file is listed as created.
func TestAdopt_Negative_UnchangedReadoptionReplacesNothing(t *testing.T) {
	repoPath, source := adoptedRepo(t, "vendor-unchanged")
	for _, force := range []bool{false, true} {
		rep := readoptWith(t, repoPath, source, AdoptOptions{Force: force})
		if replaced := rep.Replaced(); len(replaced) != 0 {
			t.Fatalf("force %v: unchanged files replaced: %+v", force, replaced)
		}
		for _, rel := range lockedPaths {
			if !fileExists(filepath.Join(repoPath, filepath.FromSlash(rel))) {
				t.Fatalf("fixture lacks %s", rel)
			}
			assertNotReplacedOrCreated(t, rep, rel)
		}
	}
	if _, err := os.Lstat(filepath.Join(repoPath, filepath.FromSlash(adoptBackupRoot))); !os.IsNotExist(err) {
		t.Fatalf("backup root created without a replace (lstat err=%v)", err)
	}
}

// Boundary: a vendor file that is the compile-context projection of AGENTS.md as the run found
// it is synchronized, not replaced, when --force rewrites the harness under it: it is Praetor
// output nobody edited, only AGENTS.md changed.
func TestAdopt_Boundary_PriorProjectionIsSynchronizedNotReplaced(t *testing.T) {
	repoPath, source := adoptedRepo(t, "vendor-prior")
	agents := filepath.Join(repoPath, agentsFile)
	harness := mustRead(t, agents)
	edited := strings.Replace(harness, "Before concluding any turn:\n", "Before concluding any turn:\nLocal harness note.\n", 1)
	if edited == harness {
		t.Fatal("fixture edit did not apply")
	}
	mustWrite(t, agents, edited)
	tr := compiler.NewTranspiler()
	res, err := tr.CompileContext(t.Context(), agents)
	if err != nil {
		t.Fatal(err)
	}
	if err := tr.WriteOutputsContext(t.Context(), res, repoPath); err != nil {
		t.Fatal(err)
	}
	rep := readoptWith(t, repoPath, source, AdoptOptions{Force: true})
	// The refresh drops the hand edit inside AGENTS.md's harness, so AGENTS.md itself is a
	// replace with its delta and backup; the vendor projections of it are not.
	for _, replaced := range rep.Replaced() {
		if replaced.Path != agentsFile {
			t.Fatalf("prior projection %s replaced: %+v", replaced.Path, rep.Replaced())
		}
	}
	if !hasAction(rep, agentsFile, actionReplace) {
		t.Fatalf("the dropped harness edit must be reported as a replace: %+v", rep.ActionDetails)
	}
	if !hasAction(rep, vendorContextFile, actionReconcile) || strings.Contains(mustRead(t, filepath.Join(repoPath, vendorContextFile)), "Local harness note.") {
		t.Fatalf("vendor file not synchronized to the refreshed harness: %+v", rep.ActionDetails)
	}
}

// plantBackupRootLink makes the backup root of an adopted repository an in-root symlink and
// returns the real directory behind it.
func plantBackupRootLink(t *testing.T, repoPath string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(repoPath, workingDirPath), 0o700); err != nil {
		t.Fatal(err)
	}
	return linkInRootDir(t, repoPath, adoptBackupRoot, "shared/backups")
}

// Negative: without --force, a symlinked backup root fails adoption before its first write
// when a vendor file holds a hand edit the agent-harness step would back up; the refusal used
// to come mid-run, after the manifest, lock and catalog steps.
func TestAdopt_Negative_PlainRunRefusesSymlinkedBackupRootForVendorEdit(t *testing.T) {
	repoPath, source := adoptedRepo(t, "vendor-backup-root")
	full := filepath.Join(repoPath, vendorContextFile)
	mustWrite(t, full, mustRead(t, full)+"Local note.\n")
	shared := plantBackupRootLink(t, repoPath)
	before := snapshotTree(t, repoPath)
	_, err := Adopt(t.Context(), AdoptOptions{LockSourceRoot: source, Path: repoPath})
	if err == nil || !strings.Contains(err.Error(), "agent-harness preflight") || !strings.Contains(err.Error(), adoptBackupRoot) {
		t.Fatalf("err = %v, want an agent-harness preflight refusal naming the backup root", err)
	}
	assertTreeUnchanged(t, before, snapshotTree(t, repoPath))
	assertDirEmpty(t, shared)
}

// Boundary: with every vendor file the projection of AGENTS.md, a plain run backs nothing up,
// so the same symlinked backup root does not stop it.
func TestAdopt_Boundary_PlainRunAcceptsSymlinkedBackupRootWithoutVendorEdit(t *testing.T) {
	repoPath, source := adoptedRepo(t, "vendor-backup-root-clean")
	shared := plantBackupRootLink(t, repoPath)
	if rep := readoptWith(t, repoPath, source, AdoptOptions{}); len(rep.Replaced()) != 0 {
		t.Fatalf("replaced: %+v", rep.Replaced())
	}
	assertDirEmpty(t, shared)
}

// Positive and boundary: under --force an edited pinned catalog file is replaced with a backup
// and listed as replaced; the dry run before it plans that replace and writes nothing.
func TestAdopt_3D_ForcedEditedCatalogFileReplacedWithBackup(t *testing.T) {
	const rel = ".config/archetypes/template-seed.yaml"
	repoPath, source := adoptedRepo(t, "catalog-edit")
	full := filepath.Join(repoPath, filepath.FromSlash(rel))
	pinned := mustRead(t, full)
	edited := pinned + "# local policy note\n"
	mustWrite(t, full, edited)

	preview := readoptWith(t, repoPath, source, AdoptOptions{Force: true, DryRun: true})
	planned := replacedEntry(t, preview, rel)
	if mustRead(t, full) != edited || fileExists(backupNamedIn(t, repoPath, planned.Details)) {
		t.Fatal("dry run wrote the catalog file or its backup")
	}

	rep := readoptWith(t, repoPath, source, AdoptOptions{Force: true})
	entry := replacedEntry(t, rep, rel)
	if !strings.Contains(entry.Details, `(-1/+0 lines, removed "# local policy note")`) ||
		mustRead(t, backupNamedIn(t, repoPath, entry.Details)) != edited || mustRead(t, full) != pinned {
		t.Fatalf("catalog replace detail %q, backup or restored bytes wrong", entry.Details)
	}
}
