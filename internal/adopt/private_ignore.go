package adopt

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/cordanaLLM/praetor/internal/state"
	"github.com/cordanaLLM/praetor/internal/util"
)

// maxPrivateIgnoreDuration bounds one EnsurePrivateIgnore call: two Git probes, a manifest
// read and one .gitignore publish (HISS-02).
const maxPrivateIgnoreDuration = 30 * time.Second

// PrivateIgnoreOutcome names what EnsurePrivateIgnore did about a repository's .gitignore.
// The zero value, PrivateIgnoreUnknown, accompanies a non-nil error.
type PrivateIgnoreOutcome string

const (
	// PrivateIgnoreUnknown is the zero value; the call failed and claims nothing.
	PrivateIgnoreUnknown PrivateIgnoreOutcome = ""
	// PrivateIgnoreNoRepository: the directory is in no Git work tree, so no commit can
	// publish the ledger. Nothing was written.
	PrivateIgnoreNoRepository PrivateIgnoreOutcome = "no-repository"
	// PrivateIgnoreEffective: the repository's ignore rules already exclude the whole
	// working directory. Nothing was written.
	PrivateIgnoreEffective PrivateIgnoreOutcome = "effective"
	// PrivateIgnoreDeclined: the rules do not exclude it, but adoption.decline names
	// git-ignore, so .gitignore belongs to the operator. Nothing was written.
	PrivateIgnoreDeclined PrivateIgnoreOutcome = "declined"
	// PrivateIgnoreWritten: this call wrote the canonical private-artifact block into
	// .gitignore, and Git now excludes the working directory.
	PrivateIgnoreWritten PrivateIgnoreOutcome = "written"
)

// EnsurePrivateIgnore makes Git exclude the private working directory of repoPath, which
// must already exist as a directory. It is what standalone `state init` calls after
// seeding a ledger: that path used to write private files into a repository whose
// .gitignore did not exclude them, one `git add .` away from publication.
//
// A repository that already excludes the directory is left byte for byte alone, whatever
// form its rule takes. Otherwise the canonical block adoption maintains is merged in
// through the same writer adoption uses, unless the manifest declines git-ignore, and Git
// is asked again so that a written block that still does not take effect is an error.
func EnsurePrivateIgnore(ctx context.Context, repoPath string) (PrivateIgnoreOutcome, error) {
	if ctx == nil {
		return PrivateIgnoreUnknown, errors.New("private ignore reconciliation requires a context")
	}
	ctx, cancel := context.WithTimeout(ctx, maxPrivateIgnoreDuration)
	defer cancel()
	inRepository, err := privateIgnoreApplies(ctx, repoPath)
	if err != nil || !inRepository {
		return privateIgnoreOutcome(PrivateIgnoreNoRepository, err)
	}
	ignored, err := workingDirIgnored(ctx, repoPath)
	if err != nil || ignored {
		return privateIgnoreOutcome(PrivateIgnoreEffective, err)
	}
	declined, err := ArtifactDeclined(declaredDeclines(ctx, repoPath), "git-ignore")
	if err != nil || declined {
		return privateIgnoreOutcome(PrivateIgnoreDeclined, err)
	}
	return writePrivateIgnore(ctx, repoPath)
}

// privateIgnoreApplies reports whether repoPath sits in a Git work tree, after checking
// that the working directory the probe asks about is a real directory. The probe names the
// directory itself, and a directory-only rule such as /.workingdir/ matches nothing that
// is absent or is a symlink, so probing either would read as unignored.
func privateIgnoreApplies(ctx context.Context, repoPath string) (bool, error) {
	working, err := repoFile(repoPath, state.WorkingDirName)
	if err != nil {
		return false, err
	}
	info, err := os.Lstat(working)
	if err != nil {
		return false, fmt.Errorf("inspect private working directory: %w", err)
	}
	if !info.IsDir() {
		return false, fmt.Errorf("private working directory %s is not a directory", state.WorkingDirName)
	}
	inRepository, err := util.GitWorktreePresent(ctx, repoPath)
	if err != nil {
		return false, fmt.Errorf("detect Git work tree: %w", err)
	}
	return inRepository, nil
}

// writePrivateIgnore merges the canonical block and proves Git honours it.
func writePrivateIgnore(ctx context.Context, repoPath string) (PrivateIgnoreOutcome, error) {
	if _, _, err := writeManagedGitIgnore(ctx, repoPath, "", false); err != nil {
		return PrivateIgnoreUnknown, fmt.Errorf("write %s: %w", gitIgnoreFile, err)
	}
	ignored, err := workingDirIgnored(ctx, repoPath)
	if err != nil {
		return PrivateIgnoreUnknown, err
	}
	if !ignored {
		return PrivateIgnoreUnknown, fmt.Errorf("%s ends with the Praetor private-artifact block but Git still does not exclude %s/", gitIgnoreFile, state.WorkingDirName)
	}
	return PrivateIgnoreWritten, nil
}

// workingDirIgnored asks Git whether the repository's ignore rules exclude the working
// directory itself. The directory is probed rather than a file inside it: once Git
// excludes a directory no negation can re-include anything under it, while a probe file
// passes rules such as ".workingdir/*" then "!.workingdir/STATE.md" that still publish a
// ledger. RunGitProbe drops global configuration, so a rule only this host's
// core.excludesFile carries does not count.
func workingDirIgnored(ctx context.Context, repoPath string) (bool, error) {
	_, err := util.RunGitProbe(ctx, repoPath, 4096, "check-ignore", "--no-index", "-q", "--", state.WorkingDirName)
	if err == nil {
		return true, nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return false, nil
	}
	return false, fmt.Errorf("probe Git ignore rules for %s: %w", state.WorkingDirName, err)
}

// privateIgnoreOutcome pairs a no-write outcome with the error that may have preempted it.
func privateIgnoreOutcome(outcome PrivateIgnoreOutcome, err error) (PrivateIgnoreOutcome, error) {
	if err != nil {
		return PrivateIgnoreUnknown, err
	}
	return outcome, nil
}
