package adopt

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/contextopt"
)

// Each lock permits at most 256 profiles and 256 facets.
const maxAdoptPolicyFiles = 512

type catalogWrite struct {
	artifact config.PolicyArtifact
	path     string
	before   []byte
	exists   bool
}

// Materialize the verified pins so generated plain audit commands are usable
// without the original workstation source path or any private external layers.
func reconcilePolicyCatalog(ctx context.Context, s *adoptSession) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.opts.DryRun {
		return planPolicyCatalog(ctx, s)
	}
	policy, err := config.LoadEffectivePolicyContext(ctx, config.EffectiveOptions{
		Root: s.repoPath, CatalogRoot: s.opts.LockSourceRoot,
	})
	if err != nil {
		return fmt.Errorf("resolve adoption catalog (select --lock-source-root for missing pinned profiles): %w", err)
	}
	writes, err := prepareCatalogWrites(ctx, s, policy.CatalogArtifacts)
	if err != nil {
		return err
	}
	if err := config.ValidateCatalogProjectionContext(ctx, s.repoPath, policy.CatalogArtifacts); err != nil {
		return fmt.Errorf("validate prospective adoption catalog: %w", err)
	}
	for i := 0; i < len(writes) && i < maxAdoptPolicyFiles; i++ {
		if err := publishCatalogFile(ctx, s, writes[i]); err != nil {
			return err
		}
	}
	s.policy, err = config.LoadEffectivePolicyContext(ctx, config.EffectiveOptions{Root: s.repoPath, Audit: true})
	if err != nil {
		return fmt.Errorf("read back repository-local pinned policy: %w", err)
	}
	return nil
}

// Legacy standalone scan callers retain the existing ceiling. Planned and applied
// adoption use the same resolved ceiling as the generated CLI/MCP audit.
func adoptionScanLimit(s *adoptSession) int {
	if s.policy != nil {
		return s.policy.Policy.Complexity.MaxFuncLOC
	}
	return defaultMaxFuncLOC
}

// Inspect every destination before publishing any catalog entry. Force permits
// explicit replacement; otherwise differing user files are always preserved.
func prepareCatalogWrites(ctx context.Context, s *adoptSession, artifacts []config.PolicyArtifact) ([]catalogWrite, error) {
	if len(artifacts) > maxAdoptPolicyFiles {
		return nil, errors.New("adoption catalog exceeds 512 pinned files")
	}
	writes := make([]catalogWrite, 0, len(artifacts))
	for i := 0; i < len(artifacts) && i < maxAdoptPolicyFiles; i++ {
		artifact := artifacts[i]
		if fmt.Sprintf("%x", sha256.Sum256(artifact.Content)) != artifact.SHA256 {
			return nil, errors.New("adoption catalog snapshot differs from its verified hash")
		}
		path, err := repoFile(s.repoPath, artifact.RelativePath)
		if err != nil {
			return nil, err
		}
		before, exists, err := contextopt.ObserveSnapshot(ctx, path)
		if err != nil {
			return nil, err
		}
		if exists && !bytes.Equal(before, artifact.Content) && !s.opts.Force {
			return nil, fmt.Errorf("pinned catalog destination differs: %s; inspect it before explicit forced adoption", artifact.RelativePath)
		}
		writes = append(writes, catalogWrite{artifact: artifact, path: path, before: before, exists: exists})
	}
	return writes, nil
}

func publishCatalogFile(ctx context.Context, s *adoptSession, write catalogWrite) error {
	if write.exists && bytes.Equal(write.before, write.artifact.Content) {
		s.report.recordReconciled(write.artifact.RelativePath, "Verified unchanged repository-local pinned policy")
		return nil
	}
	if err := contextopt.EnsureDirectory(ctx, filepath.Dir(write.path), dirPerm); err != nil {
		return err
	}
	if err := contextopt.ReplaceSnapshot(ctx, write.path, write.artifact.Content, contextopt.ReplaceOptions{
		Expected: write.before, Exists: write.exists, Mode: filePerm,
	}); err != nil {
		return err
	}
	if write.exists {
		s.report.recordReconciled(write.artifact.RelativePath, "Replaced pinned policy from explicitly selected source")
	} else {
		s.report.recordCreated(write.artifact.RelativePath, "Materialized exact pinned policy for repository-local audit")
	}
	return nil
}
