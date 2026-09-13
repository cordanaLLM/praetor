package adopt

import (
	"context"
	"errors"
	"fmt"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/contextopt"
	"gopkg.in/yaml.v3"
)

func newAdoptionManifest(ctx context.Context, s *adoptSession) *config.Manifest {
	return &config.Manifest{
		Version: 1,
		Repository: config.RepositoryMetadata{
			Owner: resolveOwner(ctx, s.repoPath), Name: s.repoName, Visibility: "public",
		},
		Profiles: []string{s.arch}, Facets: s.facets,
	}
}

func planPolicyCatalog(ctx context.Context, s *adoptSession) error {
	manifest, lock, err := plannedPolicyInputs(ctx, s)
	if errors.Is(err, ErrLockSourceRequired) && !s.opts.RecordBaseline {
		s.report.recordSkipped(".config/archetypes", "Pinned policy unavailable without --lock-source-root; no effective-policy verification performed")
		return nil
	}
	if err != nil {
		return fmt.Errorf("prepare dry-run audit policy: %w", err)
	}
	s.policy, err = config.LoadEffectivePolicyInputsContext(ctx, config.EffectiveOptions{
		Root: s.repoPath, CatalogRoot: s.opts.LockSourceRoot, Audit: true,
	}, manifest, lock)
	if err != nil {
		return err
	}
	if _, err := prepareCatalogWrites(ctx, s, s.policy.CatalogArtifacts); err != nil {
		return err
	}
	if err := config.ValidateCatalogProjectionContext(ctx, s.repoPath, s.policy.CatalogArtifacts); err != nil {
		return err
	}
	s.report.recordReconciledAs(".config/archetypes", actionSkip, "Resolved prospective pinned policy without materializing files")
	return nil
}

func plannedPolicyInputs(ctx context.Context, s *adoptSession) ([]byte, []byte, error) {
	manifest, err := plannedManifestBytes(ctx, s)
	if err != nil {
		return nil, nil, err
	}
	lock, exists, err := observeAdoptionInput(ctx, s, lockFile)
	if err != nil {
		return nil, nil, err
	}
	if exists && !s.opts.Force {
		return manifest, lock, nil
	}
	if s.opts.LockSourceRoot == "" {
		return nil, nil, ErrLockSourceRequired
	}
	var decoded config.Manifest
	if err := yaml.Unmarshal(manifest, &decoded); err != nil {
		return nil, nil, err
	}
	lock, err = config.BuildLockfile(ctx, s.opts.LockSourceRoot, &decoded)
	return manifest, lock, err
}

func plannedManifestBytes(ctx context.Context, s *adoptSession) ([]byte, error) {
	data, exists, err := observeAdoptionInput(ctx, s, manifestFile)
	if err != nil {
		return nil, err
	}
	if exists && !s.opts.Force {
		return data, nil
	}
	return yaml.Marshal(newAdoptionManifest(ctx, s))
}

func observeAdoptionInput(ctx context.Context, s *adoptSession, name string) ([]byte, bool, error) {
	path, err := repoFile(s.repoPath, name)
	if err != nil {
		return nil, false, err
	}
	return contextopt.ObserveSnapshot(ctx, path)
}
