package adopt

import (
	"context"
	"errors"
	"fmt"

	"github.com/cordanaLLM/praetor/internal/config"
	"gopkg.in/yaml.v3"
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
		return verifyExistingLock(ctx, s, manifest)
	}
	if s.opts.LockSourceRoot == "" {
		if s.opts.DryRun {
			s.report.recordSkipped(lockFile, ErrLockSourceRequired.Error())
			return nil
		}
		return ErrLockSourceRequired
	}
	data, err := config.BuildLockfile(ctx, s.opts.LockSourceRoot, manifest)
	if err != nil {
		return fmt.Errorf("generate adoption lock: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.write(path, data, filePerm); err != nil {
		return err
	}
	s.report.recordCreated(lockFile, "Pinned profiles and facets to verified source content digests")
	return nil
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
	manifest := &config.Manifest{}
	if err := yaml.Unmarshal(data, manifest); err != nil {
		return nil, fmt.Errorf("parse target manifest for lock: %w", err)
	}
	if manifest.Version != 1 {
		return nil, errors.New("target manifest requires version 1")
	}
	return manifest, nil
}
