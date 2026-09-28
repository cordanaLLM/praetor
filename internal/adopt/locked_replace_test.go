package adopt

import (
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/classify"
	markdownassets "github.com/cordanaLLM/praetor/tools/markdownlint"
)

// Every audit-locked file adoption overwrites is reported as replaced, with its line delta and
// a backup, never as created: the lock, the pinned catalog, the DevContainer bundle, the
// documentation families, the Makefile documentation block and the vendor context files.

const lockedManifest = "version: 1\nprofiles: [framework]\n"

// lockedSession is backupSession for a step that resolves the manifest's framework profile.
func lockedSession(t *testing.T, files map[string]string, opts AdoptOptions) *adoptSession {
	t.Helper()
	s := backupSession(t, files, true, opts)
	s.arch, s.facets, s.repoName = "framework", resolveFacets(nil), "fixture"
	return s
}

// resetReport gives s a fresh report with opts, as a second adoption run starts with.
func resetReport(s *adoptSession, opts AdoptOptions) {
	opts.Path, opts.SkipGitValidation, opts.LockSourceRoot = s.repoPath, true, s.opts.LockSourceRoot
	s.opts = opts
	s.report = newAdoptionReport(s.repoPath, opts, classify.Result{Archetype: s.arch})
}

// replacedEntry returns the replace entry of rel, failing when there is none.
func replacedEntry(t *testing.T, rep *AdoptReport, rel string) ActionDetail {
	t.Helper()
	for _, entry := range rep.Replaced() {
		if entry.Path == rel {
			return entry
		}
	}
	t.Fatalf("%s not replaced: %+v", rel, rep.ActionDetails)
	return ActionDetail{}
}

// assertNotReplacedOrCreated fails when rel has a replace entry or is listed as created.
func assertNotReplacedOrCreated(t *testing.T, rep *AdoptReport, rel string) {
	t.Helper()
	for _, entry := range rep.Replaced() {
		if entry.Path == rel {
			t.Fatalf("%s replaced: %+v", rel, entry)
		}
	}
	if contains(rep.CreatedFiles, rel) {
		t.Fatalf("existing %s reported as created: %v", rel, rep.CreatedFiles)
	}
}

// pinnedLockSession adopts the lock once and returns the session and the pinned bytes.
func pinnedLockSession(t *testing.T) (*adoptSession, string) {
	t.Helper()
	s := lockedSession(t, map[string]string{manifestFile: lockedManifest}, AdoptOptions{})
	s.opts.LockSourceRoot = newAdoptLockSource(t)
	if err := reconcileLockfile(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	return s, mustRead(t, filepath.Join(s.repoPath, lockFile))
}

// Positive: a forced lock rebuild over an edited lock replaces it, keeps the edited bytes under
// the backup root and lists the lock as replaced with its line delta, not as created.
func TestReconcileLockfile_Positive_ForcedRebuildOverEditedLockReplacesWithBackup(t *testing.T) {
	s, pinned := pinnedLockSession(t)
	edited := pinned + "# local pin note\n"
	mustWrite(t, filepath.Join(s.repoPath, lockFile), edited)
	resetReport(s, AdoptOptions{Force: true})
	if err := reconcileLockfile(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, filepath.Join(s.repoPath, lockFile)); got != pinned {
		t.Fatalf("lock = %q, want the rebuilt pins", got)
	}
	if got := mustRead(t, backupFile(s, lockFile)); got != edited {
		t.Fatalf("backup = %q, want the edited lock", got)
	}
	entry := replacedEntry(t, s.report, lockFile)
	want := lockPinnedDetail + "; replaced existing content (-1/+0 lines, removed \"# local pin note\"); backup: " +
		path.Join(adoptBackupRoot, testBackupStamp, lockFile)
	if entry.Details != want {
		t.Fatalf("detail = %q\nwant     %q", entry.Details, want)
	}
	if contains(s.report.CreatedFiles, lockFile) {
		t.Fatalf("replaced lock listed as created: %v", s.report.CreatedFiles)
	}
}

// Negative: a forced rebuild over a lock that already holds the rebuilt pins writes nothing,
// takes no backup and lists no replace or create entry.
func TestReconcileLockfile_Negative_ForcedRebuildOverPinnedLockIsVerified(t *testing.T) {
	s, pinned := pinnedLockSession(t)
	resetReport(s, AdoptOptions{Force: true})
	if err := reconcileLockfile(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	assertNotReplacedOrCreated(t, s.report, lockFile)
	if !hasAction(s.report, lockFile, actionReconcile) || mustRead(t, filepath.Join(s.repoPath, lockFile)) != pinned {
		t.Fatalf("pinned lock not verified in place: %+v", s.report.ActionDetails)
	}
	if fileExists(backupFile(s, lockFile)) {
		t.Fatal("backup taken of an unchanged lock")
	}
}

// Boundary: a forced dry run over an edited lock plans the replace, naming the backup it would
// take, and writes neither the lock nor the backup.
func TestReconcileLockfile_Boundary_DryRunPlansReplaceWithoutBackup(t *testing.T) {
	s, pinned := pinnedLockSession(t)
	edited := pinned + "# local pin note\n"
	mustWrite(t, filepath.Join(s.repoPath, lockFile), edited)
	resetReport(s, AdoptOptions{Force: true, DryRun: true})
	if err := reconcileLockfile(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	entry := replacedEntry(t, s.report, lockFile)
	if !strings.HasSuffix(entry.Details, "backup: "+path.Join(adoptBackupRoot, testBackupStamp, lockFile)) {
		t.Fatalf("planned replace detail = %q", entry.Details)
	}
	if got := mustRead(t, filepath.Join(s.repoPath, lockFile)); got != edited {
		t.Fatalf("dry run wrote the lock: %q", got)
	}
	if _, err := os.Lstat(filepath.Join(s.repoPath, workingDirPath)); !os.IsNotExist(err) {
		t.Fatalf("dry run created the backup root (lstat err=%v)", err)
	}
}

// markdownlintConfig is the documentation family's markdownlint configuration path.
var markdownlintConfig = markdownassets.Directory + "/markdownlint-cli2.yaml"

// docsSession is a session with the documentation facet over files.
func docsSession(t *testing.T, files map[string]string, opts AdoptOptions) *adoptSession {
	t.Helper()
	s := backupSession(t, files, true, opts)
	s.facets = []string{"docs:seo-portal"}
	return s
}

// Positive: under --force a foreign markdownlint-cli2.yaml that predates adoption is reported
// as replaced, with a backup of its bytes, not as created.
func TestReconcileDocumentationGate_Positive_ForeignMarkdownlintConfigIsReplaced(t *testing.T) {
	const foreign = "# project markdownlint rules\nignores:\n  - vendor/legacy/**\n"
	s := docsSession(t, map[string]string{markdownlintConfig: foreign}, AdoptOptions{Force: true})
	if err := reconcileDocumentationGate(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	entry := replacedEntry(t, s.report, markdownlintConfig)
	if !strings.Contains(entry.Details, `removed "# project markdownlint rules"`) || mustRead(t, backupFile(s, markdownlintConfig)) != foreign {
		t.Fatalf("replace detail %q, backup not the foreign bytes", entry.Details)
	}
	if contains(s.report.CreatedFiles, markdownlintConfig) {
		t.Fatalf("foreign config listed as created: %v", s.report.CreatedFiles)
	}
	canonical, err := markdownassets.Read("markdownlint-cli2.yaml")
	if err != nil || mustRead(t, filepath.Join(s.repoPath, filepath.FromSlash(markdownlintConfig))) != string(canonical) {
		t.Fatalf("config not replaced by the canonical text (err %v)", err)
	}
}

// documentationMakefileAround wraps block in operator recipes above and below it.
func documentationMakefileAround(block string) string {
	return "build:\n\t@echo build\n\n" + block + "\ntest:\n\t@echo test\n"
}

// Boundary: restoring an edited documentation gate block under --force lists only the edited
// in-block line in the delta, keeps every operator line outside the block and backs up the
// whole Makefile.
func TestReconcileDocumentationMakefile_Boundary_DeltaListsOnlyInBlockLines(t *testing.T) {
	block := DocumentationMakefileBlock()
	edited := documentationMakefileAround(strings.Replace(block, "\t@node tools/markdownlint/verify.mjs\n", "\t@echo skipped\n", 1))
	s := backupSession(t, map[string]string{makefileName: edited}, true, AdoptOptions{Force: true})
	if err := reconcileDocumentationMakefile(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	entry := replacedEntry(t, s.report, makefileName)
	if !strings.Contains(entry.Details, `(-1/+1 lines, removed "\t@echo skipped")`) {
		t.Fatalf("delta %q does not list exactly the edited in-block line", entry.Details)
	}
	for _, outside := range []string{"build", "test"} {
		if strings.Contains(entry.Details, outside) {
			t.Fatalf("delta %q lists a line outside the block (%s)", entry.Details, outside)
		}
	}
	if got := mustRead(t, filepath.Join(s.repoPath, makefileName)); got != documentationMakefileAround(block) {
		t.Fatalf("Makefile = %q", got)
	}
	if got := mustRead(t, backupFile(s, makefileName)); got != edited {
		t.Fatalf("backup = %q", got)
	}
}

// Negative: an exact block is left alone and a first attachment is an append; neither is a
// replace. Without --force an edited block still fails and stays as it is.
func TestReconcileDocumentationMakefile_Negative_ExactOrAppendedBlockIsNoReplace(t *testing.T) {
	for name, makefile := range map[string]string{
		"exact":  documentationMakefileAround(DocumentationMakefileBlock()),
		"append": "build:\n\t@echo build\n",
	} {
		s := backupSession(t, map[string]string{makefileName: makefile}, true, AdoptOptions{Force: true})
		if err := reconcileDocumentationMakefile(t.Context(), s); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(s.report.Replaced()) != 0 || fileExists(backupFile(s, makefileName)) {
			t.Fatalf("%s: replaced: %+v", name, s.report.ActionDetails)
		}
	}
	edited := documentationMakefileAround(strings.Replace(DocumentationMakefileBlock(), "docs-lint:\n", "docs-lint: extra\n", 1))
	plain := backupSession(t, map[string]string{makefileName: edited}, true, AdoptOptions{})
	if err := reconcileDocumentationMakefile(t.Context(), plain); err == nil || mustRead(t, filepath.Join(plain.repoPath, makefileName)) != edited {
		t.Fatalf("edited block replaced without --force (err %v)", err)
	}
}

// devContainerFixture adopts the unavailable DevContainer placeholder a config-only catalog
// yields and returns the session and the placeholder bytes.
func devContainerFixture(t *testing.T) (*adoptSession, string) {
	t.Helper()
	s := lockedSession(t, map[string]string{}, AdoptOptions{})
	s.opts.LockSourceRoot = t.TempDir()
	if err := reconcileDevContainer(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	return s, mustRead(t, filepath.Join(s.repoPath, devcontainerFile))
}

// Positive: under --force an edited devcontainer.json is replaced with a backup and listed as
// replaced, not created; a dry run first plans that replace and writes nothing.
func TestReconcileDevContainer_Positive_ForcedEditIsReplacedWithBackup(t *testing.T) {
	s, placeholder := devContainerFixture(t)
	edited := strings.Replace(placeholder, `"remoteUser": "vscode"`, `"remoteUser": "root"`, 1)
	if edited == placeholder {
		t.Fatal("fixture edit did not apply")
	}
	full := filepath.Join(s.repoPath, devcontainerFile)
	mustWrite(t, full, edited)
	resetReport(s, AdoptOptions{Force: true, DryRun: true})
	if err := reconcileDevContainer(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	if replacedEntry(t, s.report, devcontainerFile); mustRead(t, full) != edited || fileExists(backupFile(s, devcontainerFile)) {
		t.Fatal("dry run wrote the DevContainer or its backup")
	}
	resetReport(s, AdoptOptions{Force: true})
	if err := reconcileDevContainer(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	entry := replacedEntry(t, s.report, devcontainerFile)
	if !strings.Contains(entry.Details, `(-1/+1 lines, removed "  \"remoteUser\": \"root\",")`) || mustRead(t, backupFile(s, devcontainerFile)) != edited {
		t.Fatalf("replace detail %q or backup wrong", entry.Details)
	}
	if mustRead(t, full) != placeholder || contains(s.report.CreatedFiles, devcontainerFile) {
		t.Fatalf("DevContainer not restored, or listed as created: %v", s.report.CreatedFiles)
	}
}

// Negative: a forced rerun over the unchanged bundle is verified, with no replace or create
// entry and no backup.
func TestReconcileDevContainer_Negative_UnchangedBundleIsVerified(t *testing.T) {
	s, _ := devContainerFixture(t)
	resetReport(s, AdoptOptions{Force: true})
	if err := reconcileDevContainer(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	assertNotReplacedOrCreated(t, s.report, devcontainerFile)
	if action, _ := actionOf(s.report, devcontainerFile); !strings.HasPrefix(action.Details, "Verified unchanged: ") {
		t.Fatalf("unchanged DevContainer reported as %+v", action)
	}
	if fileExists(backupFile(s, devcontainerFile)) {
		t.Fatal("unchanged DevContainer backed up")
	}
}

// Boundary: Praetor's own unedited placeholder is an earlier Praetor text, so the ready bundle
// that replaces it is a refresh, not a replace; its companions are created and nothing is
// backed up.
func TestReconcileDevContainer_Boundary_OwnPlaceholderIsRefreshed(t *testing.T) {
	s, _ := devContainerFixture(t)
	s.opts.LockSourceRoot = adoptBootstrapSource(t)
	resetReport(s, AdoptOptions{Force: true})
	if err := reconcileDevContainer(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	if action, _ := actionOf(s.report, devcontainerFile); len(s.report.Replaced()) != 0 ||
		!strings.HasPrefix(action.Details, "Refreshed Praetor's own unedited unavailable placeholder") {
		t.Fatalf("placeholder refresh reported as %+v (replaced %+v)", action, s.report.Replaced())
	}
	if len(s.report.CreatedFiles) < 2 || fileExists(backupFile(s, devcontainerFile)) {
		t.Fatalf("companions not created, or placeholder backed up: %v", s.report.CreatedFiles)
	}
}
