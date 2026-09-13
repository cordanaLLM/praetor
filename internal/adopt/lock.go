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
		if _, err := config.ValidateLockfile(ctx, s.repoPath, manifest); err != nil {
			return fmt.Errorf("verify existing lock: %w", err)
		}
		s.report.recordReconciled(lockFile, "Verified existing version pins and content digests")
		return nil
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

func manifestForLock(ctx context.Context, s *adoptSession) (*config.Manifest, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path, err := repoFile(s.repoPath, manifestFile)
	if err != nil {
		return nil, err
	}
	manifest := &config.Manifest{Version: 1, Profiles: []string{s.arch}, Facets: s.facets}
	if !fileExists(path) || s.opts.Force {
		return manifest, nil
	}
	data, err := readRepoFile(path)
	if err != nil {
		return nil, err
	}
	manifest = &config.Manifest{}
	if err := yaml.Unmarshal(data, manifest); err != nil {
		return nil, fmt.Errorf("parse target manifest for lock: %w", err)
	}
	if manifest.Version != 1 {
		return nil, errors.New("target manifest requires version 1")
	}
	return manifest, nil
}
