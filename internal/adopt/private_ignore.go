package adopt

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"

	"github.com/cordanaLLM/praetor/internal/compiler"
	"github.com/cordanaLLM/praetor/internal/config"
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
	return reconcilePrivateIgnore(ctx, repoPath, workingDirIgnored, state.WorkingDirName+"/")
}

// EnsureEvidenceIgnore makes Git exclude evidenceDir, the evidence directory the text register
// block sends agent evidence to (config.RegisterPolicy.EvidenceDir), on the terms of
// EnsurePrivateIgnore: an effective rule is left alone, a declined git-ignore step writes
// nothing, and otherwise the canonical block is merged in and proven. A directory the block does
// not cover therefore fails after the write, naming it. The directory need not exist;
// compiler.EvidenceIgnored asks about a path inside it. compile-context calls it before it
// renders the rule, which used to route evidence into a directory an unadopted repository did
// not ignore.
func EnsureEvidenceIgnore(ctx context.Context, repoPath, evidenceDir string) (PrivateIgnoreOutcome, error) {
	if ctx == nil {
		return PrivateIgnoreUnknown, errors.New("evidence ignore reconciliation requires a context")
	}
	canonical, err := config.CheckEvidenceDir(evidenceDir)
	if err != nil {
		return PrivateIgnoreUnknown, err
	}
	ctx, cancel := context.WithTimeout(ctx, maxPrivateIgnoreDuration)
	defer cancel()
	inRepository, err := util.GitWorktreePresent(ctx, repoPath)
	if err != nil {
		return PrivateIgnoreUnknown, fmt.Errorf("detect Git work tree: %w", err)
	}
	if !inRepository {
		return PrivateIgnoreNoRepository, nil
	}
	ignored := func(ctx context.Context, dir string) (bool, error) {
		return compiler.EvidenceIgnored(ctx, dir, canonical)
	}
	return reconcilePrivateIgnore(ctx, repoPath, ignored, canonical)
}

// ReconcileEvidenceIgnore runs EnsureEvidenceIgnore for evidenceDir in dir and writes its notice
// line to w, the decline warning included; the notice names the evidence directory's top-level
// directory (config.EvidenceRoot). The CLI's compile-context and the MCP
// standards_compile_context write call both run it before they render the text register block,
// whose evidence rule it guards.
func ReconcileEvidenceIgnore(ctx context.Context, w io.Writer, dir, evidenceDir string) error {
	outcome, err := EnsureEvidenceIgnore(ctx, dir, evidenceDir)
	if err != nil {
		return fmt.Errorf("could not make Git ignore %s in %s: %w", evidenceDir, dir, err)
	}
	line, _ := ignoreNotice(outcome, dir, config.EvidenceRoot(evidenceDir))
	if line == "" {
		return nil
	}
	if _, err := fmt.Fprintln(w, line); err != nil {
		return fmt.Errorf("write the %s ignore notice: %w", evidenceDir, err)
	}
	return nil
}

// PrivateIgnoreNotice is the line a command prints about outcome in dir: the block it wrote, or
// the warning a declined git-ignore step leaves the operator. It is empty for every other
// outcome. warning reports that the line is the warning, which a CLI prints to standard error.
func PrivateIgnoreNotice(outcome PrivateIgnoreOutcome, dir string) (line string, warning bool) {
	return ignoreNotice(outcome, dir, state.WorkingDirName)
}

// ignoreNotice is PrivateIgnoreNotice for root, the top-level directory the caller protects.
func ignoreNotice(outcome PrivateIgnoreOutcome, dir, root string) (line string, warning bool) {
	switch outcome {
	case PrivateIgnoreWritten:
		return fmt.Sprintf("Added the Praetor private-artifact block to .gitignore in %s; Git now ignores %s/", dir, root), false
	case PrivateIgnoreDeclined:
		return fmt.Sprintf("Warning: Git does not ignore %s/ in %s and adoption.decline declines git-ignore; add /%s/ to the operator-owned .gitignore",
			root, dir, root), true
	default:
		return "", false
	}
}

// reconcilePrivateIgnore is what EnsurePrivateIgnore and EnsureEvidenceIgnore share once
// repoPath is known to sit in a Git work tree: ignored answers whether the repository's rules
// already exclude protected, the slash-terminated directory the caller protects, before and
// after the write.
func reconcilePrivateIgnore(ctx context.Context, repoPath string, ignored func(context.Context, string) (bool, error), protected string) (PrivateIgnoreOutcome, error) {
	effective, err := ignored(ctx, repoPath)
	if err != nil || effective {
		return privateIgnoreOutcome(PrivateIgnoreEffective, err)
	}
	manifest, err := loadDeclaredManifest(ctx, repoPath)
	if err != nil {
		return privateIgnoreOutcome(PrivateIgnoreUnknown, err)
	}
	declined, err := ArtifactDeclined(manifestDeclines(manifest), "git-ignore")
	if err != nil || declined {
		return privateIgnoreOutcome(PrivateIgnoreDeclined, err)
	}
	return writePrivateIgnore(ctx, repoPath, ignored, protected)
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

// writePrivateIgnore merges the canonical block and proves, through ignored, that Git honours it
// for protected. It does not probe for a Kconfig-style rule; a block that already re-includes
// .config/ keeps that rule (mergeGitIgnoreRules).
func writePrivateIgnore(ctx context.Context, repoPath string, ignored func(context.Context, string) (bool, error), protected string) (PrivateIgnoreOutcome, error) {
	if _, _, err := writeManagedGitIgnore(ctx, repoPath, "", false, false); err != nil {
		return PrivateIgnoreUnknown, fmt.Errorf("write %s: %w", gitIgnoreFile, err)
	}
	effective, err := ignored(ctx, repoPath)
	if err != nil {
		return PrivateIgnoreUnknown, err
	}
	if !effective {
		return PrivateIgnoreUnknown, fmt.Errorf("%s ends with the Praetor private-artifact block but Git still does not exclude %s", gitIgnoreFile, protected)
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
