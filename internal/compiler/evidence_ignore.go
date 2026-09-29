package compiler

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/util"
)

// evidenceProbe is the path EvidenceIgnored asks Git about: a file inside config.EvidenceDir,
// which nothing creates. Git treats every leading directory of a file path as a directory,
// so a directory-only rule such as /.workingdir/ matches whether or not the directory exists
// yet, and rules that re-include the directory, such as .workingdir/* followed by
// !.workingdir/evidence/, read as not ignored. Git cannot re-include anything under an
// excluded directory, so /.workingdir/ followed by the same negation still reads as ignored.
const evidenceProbe = config.EvidenceDir + "PRAETOR-EVIDENCE-PROBE"

// ErrEvidenceNotIgnored is returned by CheckEvidenceIgnored when the repository's ignore rules
// do not exclude config.EvidenceDir, where the text register block sends agent evidence.
var ErrEvidenceNotIgnored = errors.New("git does not ignore " + config.EvidenceDir)

// EvidenceIgnored reports whether the ignore rules of the Git work tree holding dir exclude
// config.EvidenceDir under dir. Only the repository's own rules count: util.GitIgnoredPaths
// isolates the global and system configuration, so a personal excludes file on one machine
// cannot prove the repository keeps evidence private. dir must sit in a Git work tree.
func EvidenceIgnored(ctx context.Context, dir string) (bool, error) {
	ignored, err := util.GitIgnoredPaths(ctx, dir, []string{evidenceProbe}, true)
	if err != nil {
		return false, fmt.Errorf("probe Git ignore rules for %s: %w", config.EvidenceDir, err)
	}
	return slices.Contains(ignored, evidenceProbe), nil
}

// CheckEvidenceIgnored fails with ErrEvidenceNotIgnored when dir sits in a Git work tree whose
// ignore rules do not exclude config.EvidenceDir. A directory in no work tree passes: no commit
// can publish what an agent writes there. compile-context --verify and audit both run it,
// whichever facets the manifest enables, and the error names the remedy for either way the
// repository owns its .gitignore.
func CheckEvidenceIgnored(ctx context.Context, dir string) error {
	inRepository, err := util.GitWorktreePresent(ctx, dir)
	if err != nil {
		return fmt.Errorf("detect Git work tree for %s: %w", config.EvidenceDir, err)
	}
	if !inRepository {
		return nil
	}
	ignored, err := EvidenceIgnored(ctx, dir)
	if err != nil {
		return err
	}
	if ignored {
		return nil
	}
	return fmt.Errorf("%w in %s, where the text register block sends agent evidence: run 'praetorctl compile-context' to add the "+
		"Praetor private-artifact block to .gitignore, or, where adoption.decline declines git-ignore, add /.workingdir/ to the "+
		"operator-owned .gitignore", ErrEvidenceNotIgnored, dir)
}
