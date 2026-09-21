package adopt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/cordanaLLM/praetor/internal/cavemansource"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/paperclip"
	"gopkg.in/yaml.v3"
)

// newAdoptionManifest builds the manifest adoption writes. Identity comes from the origin
// remote and stays empty otherwise. Visibility is a forge setting adoption cannot observe
// offline, so it is left unset rather than declared public.
func newAdoptionManifest(ctx context.Context, s *adoptSession) (*config.Manifest, error) {
	sources, err := adoptionRegisterSources(ctx, s)
	if err != nil {
		return nil, err
	}
	return &config.Manifest{
		Version:    1,
		Repository: config.RepositoryMetadata{Owner: s.identity.owner, Name: s.identity.name},
		Profiles:   []string{s.arch}, Facets: s.facets,
		Register: &config.RegisterPolicy{Sources: sources},
	}, nil
}

func adoptionRegisterSources(ctx context.Context, s *adoptSession) (*config.RegisterSources, error) {
	harness, err := paperclip.SynthesizeHarness(ctx, s.repoPath)
	if err != nil {
		return nil, fmt.Errorf("synthesize source coverage harness: %w", err)
	}
	data, err := json.Marshal(harness)
	if err != nil {
		return nil, fmt.Errorf("marshal source coverage harness: %w", err)
	}
	inputs := []config.RegisterSourceInput{
		{Path: paperclipFile, Surface: config.SurfacePrompts, Kind: "message", Format: config.SourceFormatJSON, Selector: "operating_contract.*"},
		{Path: paperclipFile, Surface: config.SurfacePrompts, Kind: "message", Format: config.SourceFormatJSON, Selector: "invariants.*"},
	}
	coverage, err := cavemansource.CoverageFromDocuments(inputs, map[string][]byte{paperclipFile: data})
	if err != nil {
		return nil, fmt.Errorf("compute source coverage harness: %w", err)
	}
	return &config.RegisterSources{Expected: len(coverage.Sources), SHA256: coverage.SHA256, Inputs: inputs}, nil
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
	decoded, err := config.DecodeManifest(manifest)
	if err != nil {
		return nil, nil, err
	}
	lock, err = config.BuildLockfile(ctx, s.opts.LockSourceRoot, decoded)
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
	manifest, err := newAdoptionManifest(ctx, s)
	if err != nil {
		return nil, err
	}
	return yaml.Marshal(manifest)
}

func observeAdoptionInput(ctx context.Context, s *adoptSession, name string) ([]byte, bool, error) {
	path, err := repoFile(s.repoPath, name)
	if err != nil {
		return nil, false, err
	}
	return contextopt.ObserveSnapshot(ctx, path)
}
