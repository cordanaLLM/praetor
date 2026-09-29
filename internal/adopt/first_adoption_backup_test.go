package adopt

import (
	"path/filepath"
	"strings"
	"testing"
)

// A first adoption keeps a backup of every file it replaces (#597): the git-ignore step, which
// writes the managed rule that ignores the backup root, runs before every step that can replace
// a file, and a dry run plans the same backups without writing the rule or any copy.

// firstAdoptionPriors are the existing files a first forced adoption replaces or merges into,
// with the bytes they hold: a foreign lock (lockfile step) and an editor settings file the
// editors step merges into (#551).
var firstAdoptionPriors = map[string]string{
	lockFile:                "{\"a\":1}\n",
	".vscode/settings.json": "{\"editor.tabSize\": 7}\n",
}

// firstAdoptionRepo is a Go repository without .gitignore holding firstAdoptionPriors, plus
// manifest when it is not empty.
func firstAdoptionRepo(t *testing.T, manifest string) string {
	t.Helper()
	repoPath := newTestRepo(t, "first-backup")
	mustWrite(t, filepath.Join(repoPath, "go.mod"), "module example.com/firstbackup\n\ngo 1.22\n")
	for rel, content := range firstAdoptionPriors {
		mustWrite(t, filepath.Join(repoPath, filepath.FromSlash(rel)), content)
	}
	if manifest != "" {
		mustWrite(t, filepath.Join(repoPath, manifestFile), manifest)
	}
	return repoPath
}

// backupNotedEntries returns every report entry that carries a backup note: the replaced files
// and the files merged into through replaceExisting.
func backupNotedEntries(rep *AdoptReport) map[string]ActionDetail {
	noted := make(map[string]ActionDetail)
	for _, entry := range rep.ActionDetails {
		if strings.Contains(entry.Details, "; backup: ") || strings.Contains(entry.Details, "; no backup: ") {
			noted[entry.Path] = entry
		}
	}
	return noted
}

// noBackupWarnings returns the warnings that report a refused backup.
func noBackupWarnings(rep *AdoptReport) []string {
	var found []string
	for _, warning := range rep.Warnings {
		if strings.Contains(warning, "no backup of the prior bytes") {
			found = append(found, warning)
		}
	}
	return found
}

// untrackedBackups returns the git status lines naming a path below the backup root.
func untrackedBackups(t *testing.T, repoPath string) []string {
	t.Helper()
	var found []string
	for line := range strings.SplitSeq(fixtureGit(t, repoPath, "status", "--porcelain", "--untracked-files=all"), "\n") {
		if strings.Contains(line, adoptBackupRoot) {
			found = append(found, line)
		}
	}
	return found
}

// Positive: a first forced adoption of a repository without .gitignore keeps a backup of the
// lock and of the editor settings file it merges into, with the prior bytes, warns about no
// refused backup, and git status shows no backup.
func TestAdopt_Positive_FirstForcedAdoptionBacksUpEveryReplacedFile(t *testing.T) {
	repoPath := firstAdoptionRepo(t, "")
	rep, err := Adopt(t.Context(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath, Force: true})
	if err != nil {
		t.Fatalf("adopt: %v", err)
	}
	noted := backupNotedEntries(rep)
	for rel, prior := range firstAdoptionPriors {
		entry, ok := noted[rel]
		if !ok {
			t.Fatalf("%s carries no backup note: %+v", rel, rep.ActionDetails)
		}
		at := strings.Index(entry.Details, "; backup: ")
		if at < 0 {
			t.Fatalf("%s kept no backup: %q", rel, entry.Details)
		}
		backup := strings.TrimPrefix(entry.Details[at:], "; backup: ")
		if got := mustRead(t, filepath.Join(repoPath, filepath.FromSlash(backup))); got != prior {
			t.Errorf("backup of %s = %q, want the prior bytes %q", rel, got, prior)
		}
	}
	if warned := noBackupWarnings(rep); len(warned) != 0 {
		t.Fatalf("refused backups on a first adoption: %v", warned)
	}
	if status := untrackedBackups(t, repoPath); len(status) != 0 {
		t.Fatalf("backups visible to git: %v", status)
	}
}

// Negative: with git-ignore declined and no rule of the repository's own, no backup is written
// and none appears in git status; the two refused backups are warned once, with the remedy, and
// each file's entry says it has no backup.
func TestAdopt_Negative_DeclinedGitIgnoreRefusesBackupsWithOneWarning(t *testing.T) {
	repoPath := firstAdoptionRepo(t, "version: 1\nadoption:\n  decline: [git-ignore]\n")
	rep, err := Adopt(t.Context(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath, Force: true})
	if err != nil {
		t.Fatalf("adopt: %v", err)
	}
	noted := backupNotedEntries(rep)
	for rel := range firstAdoptionPriors {
		if entry, ok := noted[rel]; !ok || !strings.Contains(entry.Details, "; no backup: ") {
			t.Errorf("%s entry = %+v, want a no-backup note", rel, entry)
		}
	}
	warned := noBackupWarnings(rep)
	if len(warned) != 1 || !strings.Contains(warned[0], "names git-ignore") || !strings.Contains(warned[0], "Warned once per run") {
		t.Fatalf("refused-backup warnings = %q, want one naming the declined git-ignore step", warned)
	}
	if fileExists(filepath.Join(repoPath, filepath.FromSlash(adoptBackupRoot))) {
		t.Fatal("backup root written although git does not ignore it")
	}
	if status := untrackedBackups(t, repoPath); len(status) != 0 {
		t.Fatalf("backups visible to git: %v", status)
	}
}

// Boundary: a forced dry run of the same first adoption plans the backups the real run takes,
// warns about no refused backup, and writes nothing: neither .gitignore nor any backup.
func TestAdopt_Boundary_FirstForcedDryRunPlansBackupsWithoutWriting(t *testing.T) {
	repoPath := firstAdoptionRepo(t, "")
	before := snapshotTree(t, repoPath)
	rep, err := Adopt(t.Context(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath, Force: true, DryRun: true})
	if err != nil {
		t.Fatalf("adopt: %v", err)
	}
	noted := backupNotedEntries(rep)
	for rel := range firstAdoptionPriors {
		if entry, ok := noted[rel]; !ok || !strings.Contains(entry.Details, "; backup: "+adoptBackupRoot+"/") {
			t.Errorf("%s planned entry = %+v, want a planned backup", rel, entry)
		}
	}
	if warned := noBackupWarnings(rep); len(warned) != 0 {
		t.Fatalf("dry run warned of refused backups the real run takes: %v", warned)
	}
	assertTreeUnchanged(t, before, snapshotTree(t, repoPath))
}

// Boundary: git-ignore follows only the manifest step, which never replaces a file, so no step
// that can replace one runs before the rule that ignores its backup exists.
func TestAdoptSteps_Boundary_GitIgnoreRunsBeforeEveryReplacingStep(t *testing.T) {
	names := adoptStepNames()
	if len(names) < 2 || names[0] != "manifest" || names[1] != "git-ignore" {
		t.Fatalf("step order starts %v, want manifest then git-ignore", names[:min(len(names), 3)])
	}
}
