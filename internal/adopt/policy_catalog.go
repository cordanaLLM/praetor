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
	"github.com/cordanaLLM/praetor/internal/hiss"
	"github.com/cordanaLLM/praetor/internal/util"
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

// Legacy standalone scan callers use the scanner's own default ceiling. Planned and applied
// adoption use the same resolved ceiling as the generated CLI/MCP audit.
func adoptionScanLimit(s *adoptSession) int {
	if s.policy != nil {
		return s.policy.Policy.Complexity.MaxFuncLOC
	}
	return hiss.DefaultMaxFuncLOC
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
	reportIgnoredCatalogWrites(ctx, s, writes)
	return writes, nil
}

// reportIgnoredCatalogWrites records an error for every pinned catalog destination the target
// repository's own ignore rules exclude. Adoption still writes the file, so a local audit
// works, but git will not commit it and a clean checkout or CI run audits without its pinned
// catalog. A kernel-style tree that ignores a bare .config swallows .config/archetypes this way,
// and git cannot re-include a path under an ignored directory, so the operator has to change
// the ignore rule itself. When git cannot answer (not installed, not a work tree) the check is
// skipped and the skip is stated as a warning, never passed off as a clean result.
func reportIgnoredCatalogWrites(ctx context.Context, s *adoptSession, writes []catalogWrite) {
	if len(writes) == 0 {
		return
	}
	paths := make([]string, 0, len(writes))
	for i := 0; i < len(writes) && i < maxAdoptPolicyFiles; i++ {
		paths = append(paths, writes[i].artifact.RelativePath)
	}
	ignored, err := util.GitIgnoredPaths(ctx, s.repoPath, paths, false)
	if err != nil {
		s.report.addWarning("%s: not checked against .gitignore, so the pinned catalog may be uncommittable: %v",
			".config/archetypes", err)
		return
	}
	for i := 0; i < len(ignored); i++ {
		s.report.addError("%s: excluded by the repository's .gitignore; adoption writes it but git will not commit it, "+
			"so a clean checkout audits without its pinned catalog. Stop ignoring the path (a negation cannot "+
			"re-include a file under an ignored directory)", ignored[i])
	}
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
