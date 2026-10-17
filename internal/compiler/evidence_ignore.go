package compiler

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/util"
)

// evidenceProbeName is the file EvidenceIgnored asks Git about inside the evidence directory,
// which nothing creates. Git treats every leading directory of a file path as a directory, so a
// directory-only rule such as /.workingdir/ matches whether or not the directory exists yet,
// and rules that re-include the directory, such as .workingdir/* followed by
// !.workingdir/evidence/, read as not ignored. Git cannot re-include anything under an
// excluded directory, so /.workingdir/ followed by the same negation still reads as ignored.
const evidenceProbeName = "PRAETOR-EVIDENCE-PROBE"

// ErrEvidenceNotIgnored is matched (errors.Is) by the error CheckEvidenceIgnored returns when
// the repository's ignore rules do not exclude the evidence directory the text register block
// sends agent evidence to (config.RegisterPolicy.EvidenceDir). The returned error names that
// directory.
var ErrEvidenceNotIgnored = errors.New("git does not ignore the agent evidence directory")

// evidenceNotIgnored is the error CheckEvidenceIgnored returns. Its text names the directory the
// manifest configures, which a sentinel with the default baked in could not.
type evidenceNotIgnored struct {
	evidenceDir string // canonical register.evidence.dir
	dir         string // directory of the AGENTS.md that carries the block
}

func (e *evidenceNotIgnored) Error() string {
	root := config.EvidenceRoot(e.evidenceDir)
	return fmt.Sprintf("git does not ignore %s in %s, where the text register block sends agent evidence: run 'praetorctl "+
		"compile-context' to add the Praetor private-artifact block to .gitignore, or, where adoption.decline declines "+
		"git-ignore or the block does not cover it, add /%s/ to the operator-owned .gitignore", e.evidenceDir, e.dir, root)
}

func (e *evidenceNotIgnored) Unwrap() error { return ErrEvidenceNotIgnored }

// EvidenceIgnored reports whether the ignore rules of the Git work tree holding dir exclude
// evidenceDir, a register.evidence.dir value relative to dir. A value config.CheckEvidenceDir
// rejects is an error, not a probe. Only the repository's own rules count:
// util.GitIgnoredPaths isolates the global and system configuration, so a personal excludes file
// on one machine cannot prove the repository keeps evidence private. dir must sit in a Git work
// tree.
func EvidenceIgnored(ctx context.Context, dir, evidenceDir string) (bool, error) {
	canonical, err := config.CheckEvidenceDir(evidenceDir)
	if err != nil {
		return false, err
	}
	probe := canonical + evidenceProbeName
	ignored, err := util.GitIgnoredPaths(ctx, dir, []string{probe}, true)
	if err != nil {
		return false, fmt.Errorf("probe Git ignore rules for %s: %w", canonical, err)
	}
	return slices.Contains(ignored, probe), nil
}

// CheckEvidenceIgnored fails with an error matching ErrEvidenceNotIgnored when dir sits in a Git
// work tree whose ignore rules do not exclude evidenceDir. A directory in no work tree passes:
// no commit can publish what an agent writes there. compile-context --verify and audit both run
// it, whichever facets the manifest enables, with the directory the manifest's register policy
// names, and the error names the remedy for either way the repository owns its .gitignore.
func CheckEvidenceIgnored(ctx context.Context, dir, evidenceDir string) error {
	canonical, err := config.CheckEvidenceDir(evidenceDir)
	if err != nil {
		return err
	}
	inRepository, err := util.GitWorktreePresent(ctx, dir)
	if err != nil {
		return fmt.Errorf("detect Git work tree for %s: %w", canonical, err)
	}
	if !inRepository {
		return nil
	}
	ignored, err := EvidenceIgnored(ctx, dir, canonical)
	if err != nil {
		return err
	}
	if ignored {
		return nil
	}
	return &evidenceNotIgnored{evidenceDir: canonical, dir: dir}
}
