package adopt

import (
	"context"
	"errors"
	"fmt"

	"github.com/cordanaLLM/praetor/internal/config"
)

// ErrLockSourceRequired prevents adoption from claiming placeholder pins are valid.
var ErrLockSourceRequired = errors.New("new lock pins require an explicit verified lock source root")

func reconcileLockfile(ctx context.Context, s *adoptSession) error {
	path, err := repoFile(s.repoPath, lockFile)
	if err != nil {
		return err
	}
	manifest, err := manifestForLock(ctx, s)
	if err != nil {
		return err
	}
	if fileExists(path) && !s.opts.Force {
		return reconcileExistingLock(ctx, s, path, manifest)
	}
	if s.opts.LockSourceRoot == "" {
		if s.opts.DryRun {
			s.report.recordSkipped(lockFile, ErrLockSourceRequired.Error())
			return nil
		}
		return ErrLockSourceRequired
	}
	if err := writeBuiltLock(ctx, s, path, manifest); err != nil {
		return err
	}
	s.report.recordCreated(lockFile, "Pinned profiles and facets to verified source content digests")
	return nil
}

// writeBuiltLock pins manifest to the selected source bundle and writes the lock unless the
// session is a dry run.
func writeBuiltLock(ctx context.Context, s *adoptSession, path string, manifest *config.Manifest) error {
	data, err := config.BuildLockfile(ctx, s.opts.LockSourceRoot, manifest)
	if err != nil {
		return fmt.Errorf("generate adoption lock: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.write(path, data, filePerm)
}

// reconcileExistingLock verifies an existing lock, or re-pins it to the selected source bundle
// when it pins only earlier Praetor catalog texts the source has since re-laid out without
// changing a value (repinsEarlierCatalog). Any other lock that disagrees with the source keeps
// failing, as before: re-pinning it is --force.
func reconcileExistingLock(ctx context.Context, s *adoptSession, path string, manifest *config.Manifest) error {
	if !repinsEarlierCatalog(ctx, s, manifest) {
		return verifyExistingLock(ctx, s, manifest)
	}
	if err := writeBuiltLock(ctx, s, path, manifest); err != nil {
		return err
	}
	s.report.recordReconciled(lockFile, "Re-pinned an unmodified earlier Praetor catalog to the selected source bundle; "+
		"its values are unchanged, only the catalog's layout moved")
	return nil
}

// repinsEarlierCatalog reports whether the existing lock disagrees with the selected source
// bundle only because it pins earlier Praetor catalog texts whose source successors hold the
// same values. The lock step and the dry-run policy plan (plannedPolicyInputs) share it, so a
// dry run plans the lock a real run writes.
func repinsEarlierCatalog(ctx context.Context, s *adoptSession, manifest *config.Manifest) bool {
	if s.opts.LockSourceRoot == "" {
		return false
	}
	_, err := config.ValidateLockfileWithOptions(ctx, config.LockValidationOptions{
		Root: s.repoPath, CatalogRoot: s.opts.LockSourceRoot,
	}, manifest)
	return errors.Is(err, config.ErrLockDigestMismatch) &&
		pinsEarlierCatalog(ctx, s.repoPath, s.opts.LockSourceRoot, manifest)
}

// pinsEarlierCatalog reports whether root's lock verifies against root's own catalog, every
// declared text there is an earlier Praetor catalog text (priorCatalogDigests), and source
// holds for each one a text with exactly its values (isLayoutOnlySuccessor). Such a repository
// holds Praetor's unedited output and re-pinning it changes layout only, so adoption re-pins
// it and the policy-catalog step refreshes its files (prepareCatalogWrites). Anything else is
// not re-pinned: a lock that does not verify locally, an edited or foreign catalog, and a
// source that changes even one value of one file. The caller then reports the mismatch
// against the source, as it did before, and taking up a changed value stays --force.
func pinsEarlierCatalog(ctx context.Context, root, source string, manifest *config.Manifest) bool {
	_, err := config.ValidateLockfileWithOptions(ctx, config.LockValidationOptions{Root: root, RequireSources: true}, manifest)
	if err != nil {
		return false
	}
	local, err := config.DeclaredCatalogTexts(ctx, root, manifest)
	if err != nil || len(local) == 0 {
		return false
	}
	next, err := config.DeclaredCatalogTexts(ctx, source, manifest)
	return err == nil && layoutOnlySuccessors(local, next)
}

// layoutOnlySuccessors reports whether next holds, entry for entry, a layout-only successor of
// every text in local (isLayoutOnlySuccessor). Both come from DeclaredCatalogTexts for one
// manifest, so they pair up by index.
func layoutOnlySuccessors(local, next []config.CatalogText) bool {
	if len(next) != len(local) || len(local) > maxAdoptPolicyFiles {
		return false
	}
	for i := 0; i < len(local) && i < maxAdoptPolicyFiles; i++ {
		if local[i].Kind != next[i].Kind || local[i].ID != next[i].ID ||
			!isLayoutOnlySuccessor(local[i].Content, next[i].Content) {
			return false
		}
	}
	return true
}

// verifyExistingLock hashes the pins against the catalog the policy-catalog step
// resolves (--lock-source-root, default the repository) and records the outcome.
func verifyExistingLock(ctx context.Context, s *adoptSession, manifest *config.Manifest) error {
	result, err := config.ValidateLockfileWithOptions(ctx, config.LockValidationOptions{
		Root: s.repoPath, CatalogRoot: s.opts.LockSourceRoot,
	}, manifest)
	if err != nil {
		return fmt.Errorf("verify existing lock: %w", err)
	}
	if !result.Verified() {
		s.report.recordReconciled(lockFile, fmt.Sprintf("Verified existing version pins and aggregate digest; %v", config.ErrLockUnverifiable))
		s.report.addWarning("%s: %v; pass --lock-source-root to verify content digests", lockFile, config.ErrLockUnverifiable)
		return nil
	}
	s.report.recordReconciled(lockFile, "Verified existing version pins and content digests")
	return nil
}

func manifestForLock(ctx context.Context, s *adoptSession) (*config.Manifest, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path, err := repoFile(s.repoPath, manifestFile)
	if err != nil {
		return nil, err
	}
	// The synthesized manifest is for a repository that has none. Where one exists it is read
	// even under --force, so a forced run re-pins the lock to what the repository *declares*
	// rather than to what detection guesses. That is also the only way to take up a newly
	// published archetype: declare it, then force the lock to be rebuilt against it.
	if !fileExists(path) {
		return &config.Manifest{Version: 1, Profiles: []string{s.arch}, Facets: s.facets}, nil
	}
	data, err := readRepoFile(path)
	if err != nil {
		return nil, err
	}
	// The policy loader's decoder, so the lock is never pinned to a manifest, or to the first
	// document of one, that the audit then refuses (BUG-857).
	manifest, err := config.DecodeManifest(data)
	if err != nil {
		return nil, fmt.Errorf("parse target manifest for lock: %w", err)
	}
	if manifest.Version != 1 {
		return nil, errors.New("target manifest requires version 1")
	}
	return manifest, nil
}
