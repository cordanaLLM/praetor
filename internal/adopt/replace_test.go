package adopt

import (
	"os"
	"path"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/agenthook"
	"github.com/cordanaLLM/praetor/internal/classify"
	"github.com/cordanaLLM/praetor/internal/testsupport"
	"github.com/cordanaLLM/praetor/internal/util"
)

// testBackupStamp fixes a hand-built session's backup directory, so a dry run and the run it
// previews name the same backup.
const testBackupStamp = "20260928T120000.000000000Z"

// scrubBackupStamp replaces the run stamp in a backup path with <stamp>, so a golden that
// records report details does not depend on the clock.
func scrubBackupStamp(text string) string {
	marker := adoptBackupRoot + "/"
	var out strings.Builder
	for range 64 {
		at := strings.Index(text, marker)
		if at < 0 {
			break
		}
		out.WriteString(text[:at+len(marker)])
		text = text[at+len(marker):]
		if end := strings.IndexByte(text, '/'); end >= 0 {
			out.WriteString("<stamp>")
			text = text[end:]
		}
	}
	out.WriteString(text)
	return out.String()
}

// fixtureGit runs one git command in dir under the hermetic fixture environment.
func fixtureGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	ctx, err := util.WithCommandEnvironment(t.Context(), testsupport.HermeticGitEnv(t))
	if err != nil {
		t.Fatal(err)
	}
	out, err := util.RunGit(ctx, dir, args...)
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return out
}

// backupSession is an adoption session over a committed git work tree holding files; with
// ignored set, .gitignore ignores the private ledger, and so the backup root, as the managed
// block does.
func backupSession(t *testing.T, files map[string]string, ignored bool, opts AdoptOptions) *adoptSession {
	t.Helper()
	repoDir := t.TempDir()
	testsupport.InitGitRepoWithOrigin(t, repoDir, "")
	if ignored {
		files[gitIgnoreFile] = "/.workingdir/\n"
	}
	for rel, content := range files {
		mustWrite(t, filepath.Join(repoDir, filepath.FromSlash(rel)), content)
	}
	fixtureGit(t, repoDir, "add", "-A")
	fixtureGit(t, repoDir, "commit", "--quiet", "-m", "fixture")
	opts.Path, opts.SkipGitValidation = repoDir, true
	return &adoptSession{
		repoPath:    repoDir,
		report:      newAdoptionReport(repoDir, opts, classify.Result{Archetype: "app-service"}),
		opts:        opts,
		backupStamp: testBackupStamp,
	}
}

// backupFile is where the session keeps the backup of rel.
func backupFile(s *adoptSession, rel string) string {
	return filepath.Join(s.repoPath, filepath.FromSlash(path.Join(adoptBackupRoot, testBackupStamp, rel)))
}

const (
	claudeOnlyManifest = "version: 1\nagent_clients: [claude]\n"
	foreignSettings    = "{\"permissions\": {\"allow\": [\"Read\"]}}\n"
)

// Positive (BUG-1026): a merge into an existing .claude/settings.json leaves no
// .claude/settings.json.bak; the prior bytes are kept under the run's backup directory, which
// git ignores, so git status shows the hook file alone and the report names the backup.
func TestReconcileAgentHooks_Positive_BackupUnderIgnoredRootLeavesTreeClean(t *testing.T) {
	s := backupSession(t, map[string]string{manifestFile: claudeOnlyManifest, claudeHookFile: foreignSettings}, true, AdoptOptions{})
	if err := reconcileAgentHooks(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	if fileExists(hookPath(s, claudeHookFile+hookBackupExt)) {
		t.Fatal("backup written beside the hook file")
	}
	if got := mustRead(t, backupFile(s, claudeHookFile)); got != foreignSettings {
		t.Fatalf("backup = %q, want the prior bytes", got)
	}
	want := "backup: " + path.Join(adoptBackupRoot, testBackupStamp, claudeHookFile)
	if action, ok := actionOf(s.report, claudeHookFile); !ok || action.Action != actionMerge || !strings.HasSuffix(action.Details, want) {
		t.Fatalf("merge report = %+v, want a detail ending %q", action, want)
	}
	if len(s.report.CreatedFiles) != 0 || len(s.report.Warnings) != 0 {
		t.Fatalf("created %v, warnings %v", s.report.CreatedFiles, s.report.Warnings)
	}
	if status := strings.TrimSpace(fixtureGit(t, s.repoPath, "status", "--porcelain")); status != "M "+claudeHookFile {
		t.Fatalf("git status --porcelain = %q, want only the hook file", status)
	}
}

// Negative: a backup root git does not ignore gets no backup and a warning, and the merge is
// still bound to the bytes it was planned from: a file edited after the plan is not published.
func TestReconcileAgentHooks_Negative_UnignoredBackupRootTakesNoBackup(t *testing.T) {
	s := backupSession(t, map[string]string{manifestFile: claudeOnlyManifest, claudeHookFile: foreignSettings}, false, AdoptOptions{})
	if err := reconcileAgentHooks(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	requireHandler(t, []byte(mustRead(t, hookPath(s, claudeHookFile))), "PreToolUse", "^Bash$", "praetorctl hook claude pre-tool", 15)
	if _, err := os.Lstat(filepath.Join(s.repoPath, workingDirPath)); !os.IsNotExist(err) {
		t.Fatalf("backup root created without an ignore rule (lstat err=%v)", err)
	}
	action, _ := actionOf(s.report, claudeHookFile)
	if !strings.Contains(action.Details, "no backup: ") || len(s.report.Warnings) != 1 ||
		!strings.Contains(s.report.Warnings[0], claudeHookFile+": no backup of the prior bytes: git does not ignore") {
		t.Fatalf("report %+v, warnings %v", action, s.report.Warnings)
	}

	bound := backupSession(t, map[string]string{manifestFile: claudeOnlyManifest, claudeHookFile: foreignSettings}, false, AdoptOptions{})
	file := agentHookFile(t, "claude")
	target, err := planHookTarget(t.Context(), bound.repoPath, "claude", file)
	if err != nil {
		t.Fatal(err)
	}
	const edited = "{\"edited\": true}\n"
	mustWrite(t, hookPath(bound, claudeHookFile), edited)
	if _, err := bound.publishHookFile(t.Context(), target); err == nil {
		t.Fatal("publish over bytes edited after the plan succeeded")
	}
	if got := mustRead(t, hookPath(bound, claudeHookFile)); got != edited {
		t.Fatalf("edited file overwritten: %q", got)
	}
}

// Negative: a backup root that is a symlink is refused before the merge writes anything, and
// nothing is written through the link.
func TestReconcileAgentHooks_Negative_SymlinkedBackupRootRefused(t *testing.T) {
	s := hookSession(t, false)
	original := `{"hooks": {}}`
	mustWrite(t, hookPath(s, claudeHookFile), original)
	victim := t.TempDir()
	if err := os.MkdirAll(filepath.Join(s.repoPath, workingDirPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Join(s.repoPath, filepath.FromSlash(adoptBackupRoot))); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := reconcileAgentHooks(t.Context(), s); err == nil || !strings.Contains(err.Error(), adoptBackupRoot) {
		t.Fatalf("err = %v, want a refusal naming the backup root", err)
	}
	if entries, err := os.ReadDir(victim); err != nil || len(entries) != 0 {
		t.Fatalf("written through the backup root link: %v (err %v)", entries, err)
	}
	if got := mustRead(t, hookPath(s, claudeHookFile)); got != original {
		t.Fatalf("hook file written after the refused backup: %q", got)
	}
}

// plantForceBackupRootFixture writes a drifted lefthook.yml, which --force replaces, and makes the
// backup root an in-root symlink, with no agent hook file for the hook preflight to plan a merge
// into. It returns the real directory behind the link.
func plantForceBackupRootFixture(t *testing.T, repoPath string) string {
	t.Helper()
	mustWrite(t, filepath.Join(repoPath, lefthookFile), "pre-commit:\n  commands:\n    local:\n      run: 'true'\n")
	if err := os.MkdirAll(filepath.Join(repoPath, workingDirPath), 0o755); err != nil {
		t.Fatal(err)
	}
	return linkInRootDir(t, repoPath, adoptBackupRoot, "shared/backups")
}

// Negative: under --force a symlinked backup root fails adoption before its first write, even
// with no hook file to merge into: a --force replace of a drifted scaffold backs up to that root
// mid-run, where the refusal used to leave a half-adopted repository. A dry run is refused the
// same way, so its preview does not report a run that would fail.
func TestAdopt_Negative_ForceRefusesSymlinkedBackupRootBeforeAnyWrite(t *testing.T) {
	for _, dryRun := range []bool{false, true} {
		repoPath := newTestRepo(t, "force-backup-root")
		shared := plantForceBackupRootFixture(t, repoPath)
		before := snapshotTree(t, repoPath)
		rep, err := Adopt(t.Context(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath, Force: true, DryRun: dryRun})
		if err == nil || !strings.Contains(err.Error(), "--force preflight") || !strings.Contains(err.Error(), adoptBackupRoot) {
			t.Fatalf("dry run %v: err = %v, want a --force preflight refusal naming the backup root", dryRun, err)
		}
		if rep == nil || len(rep.CreatedFiles) != 0 || len(rep.ReconciledFiles) != 0 {
			t.Fatalf("dry run %v: adoption reported writes before the refusal: %+v", dryRun, rep)
		}
		assertTreeUnchanged(t, before, snapshotTree(t, repoPath))
		assertDirEmpty(t, shared)
	}
}

// Boundary: without --force nothing is replaced, so the same symlinked backup root does not stop
// adoption, the drifted lefthook.yml is kept, and nothing is written behind the link.
func TestAdopt_Boundary_SymlinkedBackupRootAcceptedWithoutForce(t *testing.T) {
	repoPath := newTestRepo(t, "plain-backup-root")
	shared := plantForceBackupRootFixture(t, repoPath)
	drifted := mustRead(t, filepath.Join(repoPath, lefthookFile))
	rep, err := Adopt(t.Context(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath})
	if err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	assertNoIssues(t, rep)
	if got := mustRead(t, filepath.Join(repoPath, lefthookFile)); got != drifted || len(rep.Replaced()) != 0 {
		t.Fatalf("replaced without --force: %q, %+v", got, rep.Replaced())
	}
	assertDirEmpty(t, shared)
}

// Negative: a <file>.bak an earlier adoption left is reported, never read, moved or removed.
func TestReconcileAgentHooks_Negative_LegacyBackupReportedNotRemoved(t *testing.T) {
	s := hookSession(t, false)
	mustWrite(t, hookPath(s, claudeHookFile), foreignSettings)
	mustWrite(t, hookPath(s, claudeHookFile+hookBackupExt), "legacy\n")
	if err := reconcileAgentHooks(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, hookPath(s, claudeHookFile+hookBackupExt)); got != "legacy\n" {
		t.Fatalf("legacy backup changed: %q", got)
	}
	found := false
	for _, warning := range s.report.Warnings {
		found = found || strings.HasPrefix(warning, claudeHookFile+hookBackupExt+": backup an earlier adoption wrote")
	}
	if !found {
		t.Fatalf("legacy backup not reported: %v", s.report.Warnings)
	}
}

// Positive: --force over a drifted scaffold replaces it, keeps the prior bytes under the backup
// root and lists it as replaced with its line delta, not as created.
func TestScaffoldFile_Positive_ForceReplacesWithBackupAndDelta(t *testing.T) {
	const rel = "tools/generated.txt"
	s := backupSession(t, map[string]string{rel: "keep\nlocal edit\n"}, true, AdoptOptions{Force: true})
	state, err := s.scaffoldFile(t.Context(), scaffold{rel: rel, perm: filePerm, content: []byte("keep\ngenerated\n"),
		force: true, created: "Scaffolded fixture", verified: "verified"})
	if err != nil || state != scaffoldWritten {
		t.Fatalf("state %v, err %v", state, err)
	}
	if got := mustRead(t, filepath.Join(s.repoPath, filepath.FromSlash(rel))); got != "keep\ngenerated\n" {
		t.Fatalf("file = %q", got)
	}
	if got := mustRead(t, backupFile(s, rel)); got != "keep\nlocal edit\n" {
		t.Fatalf("backup = %q", got)
	}
	want := []ActionDetail{{Path: rel, Action: actionReplace, Details: "Scaffolded fixture; replaced existing content " +
		"(-1/+1 lines, removed \"local edit\"); backup: " + path.Join(adoptBackupRoot, testBackupStamp, rel)}}
	if got := s.report.Replaced(); !reflect.DeepEqual(got, want) {
		t.Fatalf("replaced = %+v\nwant       %+v", got, want)
	}
	if contains(s.report.CreatedFiles, rel) || !contains(s.report.ReconciledFiles, rel) || len(s.report.Warnings) != 0 {
		t.Fatalf("created %v, reconciled %v, warnings %v", s.report.CreatedFiles, s.report.ReconciledFiles, s.report.Warnings)
	}
}

// Boundary: a dry run writes nothing, takes no backup and reports the replacement it plans; the
// delta quotes at most three removed lines, each cut to a bounded length, and counts the rest.
func TestScaffoldFile_Boundary_DryRunPlansReplaceAndDeltaTruncates(t *testing.T) {
	const rel = "tools/generated.txt"
	long := strings.Repeat("x", maxDeltaLineBytes+10)
	existing := long + "\nr2\nr3\nr4\nr5\n"
	s := backupSession(t, map[string]string{rel: existing}, true, AdoptOptions{Force: true, DryRun: true})
	if _, err := s.scaffoldFile(t.Context(), scaffold{rel: rel, perm: filePerm, content: []byte("generated\n"), force: true, created: "Scaffolded fixture"}); err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, filepath.Join(s.repoPath, filepath.FromSlash(rel))); got != existing {
		t.Fatalf("dry run wrote %q", got)
	}
	if _, err := os.Lstat(filepath.Join(s.repoPath, workingDirPath)); !os.IsNotExist(err) {
		t.Fatalf("dry run created the backup root (lstat err=%v)", err)
	}
	replaced := s.report.Replaced()
	if len(replaced) != 1 {
		t.Fatalf("replaced = %+v", replaced)
	}
	detail := replaced[0].Details
	for _, want := range []string{"(-5/+1 lines, removed ", strings.Repeat("x", maxDeltaLineBytes) + "... [truncated]",
		`"r2", "r3" and 2 more)`, "; backup: " + path.Join(adoptBackupRoot, testBackupStamp, rel)} {
		if !strings.Contains(detail, want) {
			t.Errorf("detail %q lacks %q", detail, want)
		}
	}
}

// Boundary: a file that differs from the scaffold only in its line endings is verified, not
// replaced, under --force: it keeps its CRLF bytes and no backup is taken.
func TestScaffoldFile_Boundary_CRLFOnlyDifferenceIsNotReplaced(t *testing.T) {
	const rel = "tools/generated.txt"
	s := backupSession(t, map[string]string{rel: "one\r\ntwo\r\n"}, true, AdoptOptions{Force: true})
	state, err := s.scaffoldFile(t.Context(), scaffold{rel: rel, perm: filePerm, content: []byte("one\ntwo\n"), force: true, verified: "verified"})
	if err != nil || state != scaffoldIdentical {
		t.Fatalf("state %v, err %v", state, err)
	}
	if got := mustRead(t, filepath.Join(s.repoPath, filepath.FromSlash(rel))); got != "one\r\ntwo\r\n" {
		t.Fatalf("CRLF file rewritten: %q", got)
	}
	if len(s.report.Replaced()) != 0 || fileExists(backupFile(s, rel)) {
		t.Fatalf("CRLF-only difference replaced: %+v", s.report.ActionDetails)
	}

	// Mixed line endings cannot hold the scaffold's text: --force still replaces such a file,
	// with a backup, instead of preserving it as unverified.
	const mixed = "one\r\ntwo\n"
	m := backupSession(t, map[string]string{rel: mixed}, true, AdoptOptions{Force: true})
	state, err = m.scaffoldFile(t.Context(), scaffold{rel: rel, perm: filePerm, content: []byte("one\ntwo\n"), force: true, created: "Scaffolded fixture"})
	if err != nil || state != scaffoldWritten || len(m.report.Replaced()) != 1 || mustRead(t, backupFile(m, rel)) != mixed {
		t.Fatalf("mixed endings: state %v, err %v, report %+v", state, err, m.report.ActionDetails)
	}
	if detail := m.report.Replaced()[0].Details; !strings.Contains(detail, "(-0/+0 lines, line endings only)") {
		t.Fatalf("mixed-ending replace detail %q does not name the line-ending change", detail)
	}
}

// Positive, negative and boundary: a --force replace in a dry run records the bytes the run
// would write (planDryRunWrite), so the branch ruleset preview renders over the workflow the
// run leaves, not the drifted one on disk; a real run records nothing, and a dry run without
// --force, which keeps the drifted file, plans no write for it.
func TestReplaceExisting_3D_DryRunPlansTheReplacedBytes(t *testing.T) {
	const drifted = "name: docs\non: push\njobs: {}\n"
	workflow := scaffold{rel: DocumentationWorkflowFile, perm: filePerm, content: []byte(DocumentationWorkflow()),
		force: true, created: "Scaffolded fixture", verified: "verified"}

	dry := backupSession(t, map[string]string{workflow.rel: drifted}, true, AdoptOptions{Force: true, DryRun: true})
	if state, err := dry.scaffoldFile(t.Context(), workflow); err != nil || state != scaffoldWritten {
		t.Fatalf("dry run: state %v, err %v", state, err)
	}
	planned, err := dry.previewWorkflows(t.Context())
	if err != nil || string(planned[workflow.rel]) != DocumentationWorkflow() {
		t.Fatalf("the ruleset preview must read the replacing workflow, got %q, %v", planned[workflow.rel], err)
	}
	if got := mustRead(t, filepath.Join(dry.repoPath, filepath.FromSlash(workflow.rel))); got != drifted {
		t.Fatalf("dry run wrote %q", got)
	}

	applied := backupSession(t, map[string]string{workflow.rel: drifted}, true, AdoptOptions{Force: true})
	if state, err := applied.scaffoldFile(t.Context(), workflow); err != nil || state != scaffoldWritten {
		t.Fatalf("real run: state %v, err %v", state, err)
	}
	if applied.dryRunWrites != nil || mustRead(t, filepath.Join(applied.repoPath, filepath.FromSlash(workflow.rel))) != DocumentationWorkflow() {
		t.Fatalf("a real run writes the file and plans nothing: %v", applied.dryRunWrites)
	}

	kept := backupSession(t, map[string]string{workflow.rel: drifted}, true, AdoptOptions{DryRun: true})
	if state, err := kept.scaffoldFile(t.Context(), workflow); err != nil || state != scaffoldDrifted {
		t.Fatalf("dry run without --force: state %v, err %v", state, err)
	}
	if _, recorded := kept.dryRunWrites[workflow.rel]; recorded {
		t.Fatalf("a kept drifted file must plan no write: %q", kept.dryRunWrites[workflow.rel])
	}
}

// Positive, negative and boundary: describeLineDelta names a line-ending-only change when the LF
// texts match, and never when a line changed.
func TestDescribeLineDelta_LineEndingsOnly(t *testing.T) {
	if got := describeLineDelta([]byte("one\r\ntwo\n"), []byte("one\ntwo\n")); got != "-0/+0 lines, line endings only" {
		t.Fatalf("mixed endings = %q", got)
	}
	if got := describeLineDelta([]byte("one\ntwo\n"), []byte("one\nthree\n")); got != `-1/+1 lines, removed "two"` {
		t.Fatalf("edited line = %q", got)
	}
	if got := describeLineDelta([]byte("one\n"), []byte("one\nadded\n")); got != "-0/+1 lines" {
		t.Fatalf("added line = %q", got)
	}
}

// Positive, negative and boundary: quoteFirst, the one bounded listing the line delta, the
// editor merge and the lefthook skip reason share, quotes up to limit items and counts the rest
// of total, including items a bounded prefix already left out; an empty list is empty and a
// list of exactly limit items names no remainder.
func TestQuoteFirst_BoundsAndCountsTheRest(t *testing.T) {
	cases := []struct {
		items        []string
		total, limit int
		want         string
	}{
		{[]string{"a", "b", "c"}, 3, 2, `"a", "b" and 1 more`},
		{nil, 0, 3, ""},
		{[]string{"a", "b"}, 2, 2, `"a", "b"`},
		{[]string{"a", "b"}, 7, 2, `"a", "b" and 5 more`},
		{[]string{"a"}, 1, 5, `"a"`},
	}
	for _, tc := range cases {
		if got := quoteFirst(tc.items, tc.total, tc.limit, strconv.Quote); got != tc.want {
			t.Errorf("quoteFirst(%q, %d, %d) = %q, want %q", tc.items, tc.total, tc.limit, got, tc.want)
		}
	}
}

// Positive: a backup written while the private ledger directory is still absent (a fresh clone
// whose .gitignore already ignores it) creates the directory owner-only, as the ledger
// initialisation would, instead of at the writer's 0755 default.
func TestBackupExisting_Positive_CreatesPrivateWorkingDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no POSIX permission bits to check")
	}
	s := backupSession(t, map[string]string{manifestFile: claudeOnlyManifest, claudeHookFile: foreignSettings}, true, AdoptOptions{})
	if err := reconcileAgentHooks(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(s.repoPath, workingDirPath))
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got&0o077 != 0 {
		t.Fatalf("%s created with mode %v, want owner-only", workingDirPath, got)
	}
	if got := mustRead(t, backupFile(s, claudeHookFile)); got != foreignSettings {
		t.Fatalf("backup = %q", got)
	}
}

// Negative: an existing private ledger directory keeps its mode; adoption never changes the
// permissions of a directory it did not create.
func TestBackupExisting_Negative_KeepsExistingWorkingDirMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no POSIX permission bits to check")
	}
	s := backupSession(t, map[string]string{manifestFile: claudeOnlyManifest, claudeHookFile: foreignSettings}, true, AdoptOptions{})
	dir := filepath.Join(s.repoPath, workingDirPath)
	if err := os.Mkdir(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := reconcileAgentHooks(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(dir)
	if err != nil || info.Mode().Perm() != 0o750 {
		t.Fatalf("existing %s mode changed: %v, %v", workingDirPath, info, err)
	}
}

// Boundary: when git cannot answer (no work tree), the report note says the ignore rule could
// not be confirmed rather than claiming git does not ignore the backup path, and the warning
// carries git's error.
func TestBackupExisting_Boundary_GitFailureNamedInNote(t *testing.T) {
	s := hookSession(t, true)
	note, err := s.backupExisting(t.Context(), claudeHookFile, []byte(foreignSettings))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(note, "no backup: git could not confirm ") || strings.Contains(note, "is not ignored by git") {
		t.Fatalf("note = %q", note)
	}
	if len(s.report.Warnings) != 1 || !strings.Contains(s.report.Warnings[0], "is ignored: ") {
		t.Fatalf("warnings = %v", s.report.Warnings)
	}
}

// Positive, negative and boundary: Replaced returns the replace entries alone, in order, and an
// empty list for a report without one.
func TestAdoptReport_Replaced(t *testing.T) {
	rep := &AdoptReport{}
	rep.recordReplaced("a", "first")
	rep.recordCreated("b", "created")
	rep.recordReconciledAs("c", actionMerge, "merged")
	rep.recordReplaced("d", "second")
	want := []ActionDetail{{Path: "a", Action: actionReplace, Details: "first"}, {Path: "d", Action: actionReplace, Details: "second"}}
	if got := rep.Replaced(); !reflect.DeepEqual(got, want) {
		t.Fatalf("replaced = %+v", got)
	}
	if !reflect.DeepEqual(rep.ReconciledFiles, []string{"a", "c", "d"}) || !reflect.DeepEqual(rep.CreatedFiles, []string{"b"}) {
		t.Fatalf("created %v, reconciled %v", rep.CreatedFiles, rep.ReconciledFiles)
	}
	if got := (&AdoptReport{}).Replaced(); got == nil || len(got) != 0 {
		t.Fatalf("empty report replaced = %#v", got)
	}
}

// Positive: ReconciledNotReplaced drops every file with a replace entry and keeps the merged,
// verified and refreshed files in report order.
func TestAdoptReport_Positive_ReconciledNotReplacedDropsReplaced(t *testing.T) {
	rep := &AdoptReport{}
	rep.recordReplaced("a", "first")
	rep.recordReconciledAs("c", actionMerge, "merged")
	rep.recordReconciled("e", "verified")
	rep.recordReplaced("d", "second")
	if got := rep.ReconciledNotReplaced(); !reflect.DeepEqual(got, []string{"c", "e"}) {
		t.Fatalf("reconciled not replaced = %v", got)
	}
	if !reflect.DeepEqual(rep.ReconciledFiles, []string{"a", "c", "e", "d"}) {
		t.Fatalf("report reconciled files changed: %v", rep.ReconciledFiles)
	}
}

// Negative: a report without a replace entry keeps every reconciled file; a created file never
// appears among them.
func TestAdoptReport_Negative_ReconciledNotReplacedWithoutReplace(t *testing.T) {
	rep := &AdoptReport{}
	rep.recordCreated("b", "created")
	rep.recordReconciledAs("c", actionMerge, "merged")
	rep.recordReconciled("e", "verified")
	if got := rep.ReconciledNotReplaced(); !reflect.DeepEqual(got, []string{"c", "e"}) {
		t.Fatalf("reconciled not replaced = %v", got)
	}
}

// Boundary: an empty report, and one whose every reconciled file was replaced, yield an empty,
// non-nil list.
func TestAdoptReport_Boundary_ReconciledNotReplacedEmpty(t *testing.T) {
	if got := (&AdoptReport{}).ReconciledNotReplaced(); got == nil || len(got) != 0 {
		t.Fatalf("empty report = %#v", got)
	}
	rep := &AdoptReport{}
	rep.recordReplaced("a", "first")
	if got := rep.ReconciledNotReplaced(); got == nil || len(got) != 0 {
		t.Fatalf("all replaced = %#v", got)
	}
}

// Boundary: a session built without a stamp fixes one on first use and keeps it, so every
// backup of one run lands in one directory.
func TestBackupPath_Boundary_StampFixedOnFirstUse(t *testing.T) {
	s := &adoptSession{}
	first, second := s.backupPath("a/b.json"), s.backupPath("c.json")
	if s.backupStamp == "" || first != path.Join(adoptBackupRoot, s.backupStamp, "a/b.json") ||
		second != path.Join(adoptBackupRoot, s.backupStamp, "c.json") || strings.ContainsAny(s.backupStamp, `:\`) {
		t.Fatalf("stamp %q, paths %q %q", s.backupStamp, first, second)
	}
	if got := scrubBackupStamp("backup: " + first); got != "backup: "+adoptBackupRoot+"/<stamp>/a/b.json" {
		t.Fatalf("scrub = %q", got)
	}
}

// agentHookFile returns client's native hook file from the registration table.
func agentHookFile(t *testing.T, client string) agenthook.HookFile {
	t.Helper()
	file, ok := agenthook.NativeHookFile(client)
	if !ok {
		t.Fatalf("no native hook file for %s", client)
	}
	return file
}
