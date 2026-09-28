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

// newAdoptionManifest builds the manifest adoption writes, plus the harness plan its
// register.sources came from. Identity comes from the origin remote and stays empty
// otherwise. Visibility is a forge setting adoption cannot observe offline, so it is left
// unset rather than declared public.
func newAdoptionManifest(ctx context.Context, s *adoptSession) (*config.Manifest, harnessPlan, error) {
	plan, err := planHarness(ctx, s)
	if err != nil {
		return nil, harnessPlan{}, err
	}
	manifest := &config.Manifest{
		Version:    1,
		Repository: config.RepositoryMetadata{Owner: s.identity.owner, Name: s.identity.name},
		Profiles:   []string{s.arch}, Facets: s.facets,
	}
	if plan.absent() {
		return manifest, plan, nil
	}
	sources, err := managedRegisterSources(ctx, plan.data)
	if err != nil {
		return nil, harnessPlan{}, err
	}
	manifest.Register = &config.RegisterPolicy{Sources: sources}
	return manifest, plan, nil
}

// managedHarnessInputs are the register.sources rows adoption declares for the Paperclip
// harness it owns.
func managedHarnessInputs() []config.RegisterSourceInput {
	return []config.RegisterSourceInput{
		{Path: paperclipFile, Surface: config.SurfacePrompts, Kind: "message", Format: config.SourceFormatJSON, Selector: "operating_contract.*"},
		{Path: paperclipFile, Surface: config.SurfacePrompts, Kind: "message", Format: config.SourceFormatJSON, Selector: "invariants.*"},
	}
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
	// rules reports whether writing the harness also writes rules.md.
	rules bool
	// onDisk reports whether a harness file exists before this run writes one.
	onDisk bool
	// unresolved reports a run without a repository identity: the harness platform names the
	// repository, so none is synthesized and an existing harness stays as it is (BUG-852).
	unresolved bool
}

// absent reports a harness that neither exists nor is written by this run: the paperclip
// step is declined or the repository identity is unresolved, and no harness is on disk, so
// there is nothing to bind register.sources to.
func (p harnessPlan) absent() bool {
	return !p.onDisk && p.write == nil
}

func planHarness(ctx context.Context, s *adoptSession) (harnessPlan, error) {
	path, err := repoFile(s.repoPath, paperclipFile)
	if err != nil {
		return harnessPlan{}, err
	}
	exists := fileExists(path)
	if s.declines("paperclip") {
		return keptHarnessPlan(ctx, path, exists, false)
	}
	synthesized, fresh, err := synthesizeHarness(ctx, s.repoPath)
	if errors.Is(err, util.ErrRepoIdentityUnresolved) {
		return keptHarnessPlan(ctx, path, exists, true)
	}
	if err != nil {
		return harnessPlan{}, err
	}
	if !exists || s.opts.Force {
		return harnessPlan{data: fresh, write: synthesized, rules: true, onDisk: exists}, nil
	}
	existing, err := existingHarness(ctx, path)
	if err != nil || bytes.Equal(existing, fresh) {
		return harnessPlan{data: existing, onDisk: true}, err
	}
	return planEarlierHarness(ctx, s.repoPath, existing, synthesized, fresh)
}

// keptHarnessPlan never plans a write. A declined paperclip step does not run, and a run
// without a repository identity has no platform to synthesize (BUG-852), so a planned
// harness would bind the manifest to a file nothing produces. An existing harness stays
// byte for byte; with none on disk the plan is absent.
func keptHarnessPlan(ctx context.Context, path string, exists, unresolved bool) (harnessPlan, error) {
	if !exists {
		return harnessPlan{unresolved: unresolved}, nil
	}
	existing, err := existingHarness(ctx, path)
	if err != nil {
		return harnessPlan{}, err
	}
	return harnessPlan{data: existing, onDisk: true, unresolved: unresolved}, nil
}

func synthesizeHarness(ctx context.Context, repoPath string) (*paperclip.Harness, []byte, error) {
	synthesized, err := paperclip.SynthesizeHarness(ctx, repoPath)
	if err != nil {
		return nil, nil, fmt.Errorf("synthesize paperclip harness: %w", err)
	}
	fresh, err := paperclip.MarshalHarness(synthesized)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal paperclip harness: %w", err)
	}
	return synthesized, fresh, nil
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
	if prior.Generated {
		return harnessPlan{data: fresh, write: synthesized, refresh: true, rules: prior.Rules, onDisk: true}, nil
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
	decoded, err := config.DecodeManifest(manifest)
	if err != nil {
		return nil, nil, err
	}
	if exists && !s.opts.Force && !repinsEarlierCatalog(ctx, s, decoded) {
		return manifest, lock, nil
	}
	if s.opts.LockSourceRoot == "" {
		return nil, nil, ErrLockSourceRequired
	}
	lock, err = config.BuildLockfile(ctx, s.opts.LockSourceRoot, decoded)
	return manifest, lock, err
}

// plannedManifestBytes is the manifest reconcileManifest leaves on disk. --force never
// rewrites an existing manifest, so it plans from that manifest too.
func plannedManifestBytes(ctx context.Context, s *adoptSession) ([]byte, error) {
	data, exists, err := observeAdoptionInput(ctx, s, manifestFile)
	if err != nil {
		return nil, err
	}
	if exists {
		path, err := repoFile(s.repoPath, manifestFile)
		if err != nil {
			return nil, err
		}
		planned, _, err := planExistingManifest(ctx, s, path, data)
		return planned.data, err
	}
	manifest, _, err := newAdoptionManifest(ctx, s)
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
