package adopt

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/cordanaLLM/praetor/internal/cavemansource"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/paperclip"
	"github.com/cordanaLLM/praetor/internal/util"
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

// managedHarnessInputs are the register.sources rows adoption declares for the Paperclip
// harness it owns.
func managedHarnessInputs() []config.RegisterSourceInput {
	return []config.RegisterSourceInput{
		{Path: paperclipFile, Surface: config.SurfacePrompts, Kind: "message", Format: config.SourceFormatJSON, Selector: "operating_contract.*"},
		{Path: paperclipFile, Surface: config.SurfacePrompts, Kind: "message", Format: config.SourceFormatJSON, Selector: "invariants.*"},
	}
}

func adoptionRegisterSources(ctx context.Context, s *adoptSession) (*config.RegisterSources, error) {
	plan, err := planHarness(ctx, s)
	if err != nil {
		return nil, err
	}
	return managedRegisterSources(ctx, plan.data)
}

func managedRegisterSources(ctx context.Context, harness []byte) (*config.RegisterSources, error) {
	inputs := managedHarnessInputs()
	coverage, err := cavemansource.CoverageFromDocuments(ctx, inputs, map[string][]byte{paperclipFile: harness})
	if err != nil {
		return nil, fmt.Errorf("compute source coverage harness: %w", err)
	}
	return &config.RegisterSources{Expected: coverage.Applicable, NotApplicable: coverage.NotApplicable,
		SHA256: coverage.SHA256, Inputs: inputs}, nil
}

// harnessPlan is the Paperclip harness adoption leaves on disk. data is what register.sources
// binds to; write is the synthesized harness this run writes, nil when the existing one stays.
// The manifest and paperclip steps both read it, so the bytes the contract binds cannot
// drift from the bytes adoption writes.
type harnessPlan struct {
	data    []byte
	write   *paperclip.Harness
	refresh bool
	// onDisk reports whether a harness file exists before this run writes one.
	onDisk bool
	// unresolved reports a run without a repository identity: the harness platform names the
	// repository, so none is synthesized and an existing harness stays as it is (BUG-852).
	unresolved bool
}

func planHarness(ctx context.Context, s *adoptSession) (harnessPlan, error) {
	path, err := repoFile(s.repoPath, paperclipFile)
	if err != nil {
		return harnessPlan{}, err
	}
	exists, declined := fileExists(path), s.declines("paperclip")
	synthesized, err := paperclip.SynthesizeHarness(ctx, s.repoPath)
	if errors.Is(err, util.ErrRepoIdentityUnresolved) {
		return unresolvedHarnessPlan(ctx, path, exists)
	}
	if err != nil {
		return harnessPlan{}, fmt.Errorf("synthesize paperclip harness: %w", err)
	}
	fresh, err := paperclip.MarshalHarness(synthesized)
	if err != nil {
		return harnessPlan{}, fmt.Errorf("marshal paperclip harness: %w", err)
	}
	if !exists || (s.opts.Force && !declined) {
		return harnessPlan{data: fresh, write: synthesized, onDisk: exists}, nil
	}
	existing, err := existingHarness(ctx, path)
	if err != nil || bytes.Equal(existing, fresh) || declined {
		return harnessPlan{data: existing, onDisk: true}, err
	}
	return planEarlierHarness(ctx, s.repoPath, existing, synthesized, fresh)
}

// unresolvedHarnessPlan keeps an existing harness as the bytes register.sources binds and
// plans none otherwise: without a repository identity there is no platform to synthesize.
func unresolvedHarnessPlan(ctx context.Context, path string, exists bool) (harnessPlan, error) {
	if !exists {
		return harnessPlan{unresolved: true}, nil
	}
	existing, err := existingHarness(ctx, path)
	return harnessPlan{data: existing, onDisk: true, unresolved: true}, err
}

// planEarlierHarness refreshes an existing harness only when it is unmodified output of an
// earlier release; any other harness is operator-owned and stays byte for byte.
func planEarlierHarness(ctx context.Context, repoPath string, existing []byte, synthesized *paperclip.Harness,
	fresh []byte,
) (harnessPlan, error) {
	prior, err := paperclip.PriorGenerated(ctx, repoPath, synthesized)
	if err != nil {
		return harnessPlan{}, fmt.Errorf("compare existing paperclip harness with earlier output: %w", err)
	}
	if prior {
		return harnessPlan{data: fresh, write: synthesized, refresh: true, onDisk: true}, nil
	}
	return harnessPlan{data: existing, onDisk: true}, nil
}

func existingHarness(ctx context.Context, path string) ([]byte, error) {
	if _, err := paperclip.LoadHarnessContext(ctx, path); err != nil {
		return nil, fmt.Errorf("validate existing source coverage harness: %w", err)
	}
	data, err := contextopt.ReadSnapshot(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("read existing source coverage harness: %w", err)
	}
	return data, nil
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
		path, err := repoFile(s.repoPath, manifestFile)
		if err != nil {
			return nil, err
		}
		planned, _, err := planExistingManifest(ctx, s, path, data)
		return planned.data, err
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
