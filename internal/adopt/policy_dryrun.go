package adopt

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/cordanaLLM/praetor/internal/cavemansource"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/hisscatalog"
	"github.com/cordanaLLM/praetor/internal/paperclip"
	"github.com/cordanaLLM/praetor/internal/util"
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
	manifest := declaredAdoptionManifest(s)
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

// declaredAdoptionManifest is the manifest adoption writes before register.sources binds the
// Paperclip harness: identity, the default branch only this checkout records (s.defaultBranch),
// profile and facets, every declaration the effective policy reads.
func declaredAdoptionManifest(s *adoptSession) *config.Manifest {
	return &config.Manifest{
		Version: 1,
		Repository: config.RepositoryMetadata{Owner: s.identity.owner, Name: s.identity.name,
			DefaultBranch: s.defaultBranch},
		Profiles: []string{s.arch}, Facets: s.facets,
	}
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
	// neverWrites reports a plan that writes no harness whatever is on disk: the paperclip step
	// is declined or the repository identity is unresolved (keptHarnessPlan). Deleting the
	// harness then regenerates nothing, so no remedy may suggest it.
	neverWrites bool
	// owned holds the bytes of an operator-owned harness, one that is neither the current
	// synthesis nor unmodified earlier output. Adoption keeps it, under --force too (#502):
	// data equals owned unless --force set its platform (patched).
	owned []byte
	// platform is the platform the current synthesis names when an operator-owned harness names
	// another: the value --force sets, which a plain run only reports.
	platform string
	// unpatched says why the platform of an operator-owned harness could be neither compared
	// nor set (paperclip.PatchPlatform), in either mode; the harness then stays as written.
	unpatched string
}

// absent reports a harness that neither exists nor is written by this run: the paperclip
// step is declined or the repository identity is unresolved, and no harness is on disk, so
// there is nothing to bind register.sources to.
func (p harnessPlan) absent() bool {
	return !p.onDisk && p.write == nil
}

// writesOverAbsent reports a harness this run synthesizes where no file exists: its bytes are
// Praetor output, never an operator's edit.
func (p harnessPlan) writesOverAbsent() bool {
	return !p.onDisk && p.write != nil
}

// patched reports an operator-owned harness whose platform this --force run sets: data holds
// the patched bytes it writes over owned.
func (p harnessPlan) patched() bool {
	return p.owned != nil && !bytes.Equal(p.data, p.owned)
}

// writes reports whether this run writes harness bytes, a synthesis or a platform patch, so a
// declared register.sources contract has to follow them.
func (p harnessPlan) writes() bool {
	return p.write != nil || p.patched()
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
	synthesized, fresh, err := synthesizeHarness(ctx, s.repoPath, s.paperclipFacts(ctx))
	if errors.Is(err, util.ErrRepoIdentityUnresolved) {
		return keptHarnessPlan(ctx, path, exists, true)
	}
	if err != nil {
		return harnessPlan{}, err
	}
	if !exists {
		// A rules.md without a harness is the operator's: the new harness is written beside it
		// and the page is kept, rather than overwritten unreported (#366).
		rules, err := repoFile(s.repoPath, paperclipRulesFile)
		if err != nil {
			return harnessPlan{}, err
		}
		return harnessPlan{data: fresh, write: synthesized, rules: !fileExists(rules)}, nil
	}
	existing, err := existingHarness(ctx, path)
	if err != nil || bytes.Equal(existing, fresh) {
		return harnessPlan{data: existing, onDisk: true}, err
	}
	plan, err := planEarlierHarness(ctx, s.repoPath, existing, synthesized, fresh)
	if err != nil || plan.owned == nil {
		return plan, err
	}
	return planOwnedHarness(plan, synthesized.Platform, s.opts.Force), nil
}

// planOwnedHarness compares the platform of an operator-owned harness with the one the current
// synthesis names, the value audit requires. --force sets a differing platform and nothing
// else (paperclip.PatchPlatform); a plain run keeps the bytes and records the platform so the
// paperclip step can say what --force would set. A harness PatchPlatform cannot decode stays
// as written either way, and the reason is recorded in both modes: its platform goes unchecked,
// so a plain run would otherwise leave a mismatch for audit to find.
func planOwnedHarness(plan harnessPlan, platform string, force bool) harnessPlan {
	patched, changed, err := paperclip.PatchPlatform(plan.owned, platform)
	if err != nil {
		plan.unpatched = err.Error()
		return plan
	}
	if !changed {
		return plan
	}
	plan.platform = platform
	if force {
		plan.data = patched
	}
	return plan
}

// keptHarnessPlan never plans a write. A declined paperclip step does not run, and a run
// without a repository identity has no platform to synthesize (BUG-852), so a planned
// harness would bind the manifest to a file nothing produces. An existing harness stays
// byte for byte; with none on disk the plan is absent.
func keptHarnessPlan(ctx context.Context, path string, exists, unresolved bool) (harnessPlan, error) {
	if !exists {
		return harnessPlan{unresolved: unresolved, neverWrites: true}, nil
	}
	existing, err := existingHarness(ctx, path)
	if err != nil {
		return harnessPlan{}, err
	}
	return harnessPlan{data: existing, onDisk: true, unresolved: unresolved, neverWrites: true}, nil
}

// synthesizeHarness renders the Paperclip harness for the repository's HISS facts
// (paperclipFacts). Every fact is fixed before or at the run's first harness plan, so the
// manifest step, which binds register.sources to these bytes, and the paperclip step, which
// writes them, render the same harness.
func synthesizeHarness(ctx context.Context, repoPath string, facts hisscatalog.Facts) (*paperclip.Harness, []byte, error) {
	synthesized, err := paperclip.SynthesizeHarness(ctx, repoPath, facts)
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
// earlier release, and rewrites rules.md only when it exists (PriorState.Rules), so one the
// operator removed stays removed. Any other harness is operator-owned: --force does not
// regenerate it (planOwnedHarness); deleting it and re-running adopt does.
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
	return harnessPlan{data: existing, onDisk: true, owned: existing}, nil
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
	s.policy, err = resolvePlannedPolicy(ctx, s, manifest, lock)
	if err != nil {
		return err
	}
	writes, err := prepareCatalogWrites(ctx, s, s.policy.CatalogArtifacts)
	if err != nil {
		return err
	}
	if err := config.ValidateCatalogProjectionContext(ctx, s.repoPath, s.policy.CatalogArtifacts); err != nil {
		return err
	}
	return publishCatalogWrites(ctx, s, writes)
}

func plannedPolicyInputs(ctx context.Context, s *adoptSession) ([]byte, []byte, error) {
	manifest, err := plannedManifestBytes(ctx, s)
	if err != nil {
		return nil, nil, err
	}
	lock, err := plannedLock(ctx, s, manifest)
	if err != nil {
		return nil, nil, err
	}
	return manifest, lock, nil
}

// plannedLock is the lock the lockfile step leaves on disk for the planned manifest: the
// existing one, unless --force or a re-pin of an earlier catalog rebuilds it from
// --lock-source-root, and without either one ErrLockSourceRequired.
func plannedLock(ctx context.Context, s *adoptSession, manifest []byte) ([]byte, error) {
	lock, exists, err := observeAdoptionInput(ctx, s, lockFile)
	if err != nil {
		return nil, err
	}
	decoded, err := config.DecodeManifest(manifest)
	if err != nil {
		return nil, err
	}
	if exists && !s.opts.Force && !repinsEarlierCatalog(ctx, s, decoded) {
		return lock, nil
	}
	if s.opts.LockSourceRoot == "" {
		return nil, ErrLockSourceRequired
	}
	return config.BuildLockfile(ctx, s.opts.LockSourceRoot, decoded)
}

// resolvePlannedPolicy resolves the planned manifest and lock with the loader and audit
// compatibility layer `praetorctl audit` resolves the files on disk with
// (config.LoadEffectivePolicyInputsContext, Audit), so the plan states the policy that audit
// will enforce.
func resolvePlannedPolicy(ctx context.Context, s *adoptSession, manifest, lock []byte) (*config.EffectivePolicy, error) {
	return config.LoadEffectivePolicyInputsContext(ctx, config.EffectiveOptions{
		Root: s.repoPath, CatalogRoot: s.opts.LockSourceRoot, Audit: true,
	}, manifest, lock)
}

// paperclipFacts is what the Paperclip harness's HISS invariants depend on: repositoryFacts and
// the function length the audit enforces once this run's policy resolves (harnessFuncLOC).
func (s *adoptSession) paperclipFacts(ctx context.Context) hisscatalog.Facts {
	facts := repositoryFacts(s.verification, s.exceptions)
	facts.MaxFuncLOC = s.harnessFuncLOC(ctx)
	return facts
}

// harnessFuncLOC is the function length the audit enforces after this run, for the Paperclip
// harness. The manifest step binds that harness in register.sources before the policy-catalog
// step resolves the policy, so the limit is read from the policy the planned manifest and lock
// resolve to (prospectivePolicy), the one the policy-catalog step then materializes. It is
// resolved once and kept, so every step of the run renders the bytes the manifest bound. A
// policy that does not resolve yet, such as a first adoption without --lock-source-root, leaves
// it zero: the harness then states the audit ceiling, and the policy-catalog step reports the
// cause.
func (s *adoptSession) harnessFuncLOC(ctx context.Context) int {
	if s.paperclipLimit.resolved {
		return s.paperclipLimit.limit
	}
	s.paperclipLimit.resolved = true
	if s.policy != nil {
		s.paperclipLimit.limit = adoptionScanLimit(s)
		return s.paperclipLimit.limit
	}
	if policy, err := prospectivePolicy(ctx, s); err == nil {
		s.paperclipLimit.limit = policy.Policy.Complexity.MaxFuncLOC
	}
	return s.paperclipLimit.limit
}

// prospectivePolicy resolves the policy this run leaves the repository under from the manifest
// declarations the policy reads, before register.sources is bound: the existing manifest, which
// adoption never rewrites beyond register.sources and its layout, or the one it creates
// (declaredAdoptionManifest). register.sources is no policy input (ADR-0010).
func prospectivePolicy(ctx context.Context, s *adoptSession) (*config.EffectivePolicy, error) {
	manifest, exists, err := observeAdoptionInput(ctx, s, manifestFile)
	if err != nil {
		return nil, err
	}
	if !exists {
		if manifest, err = config.RenderManifest(declaredAdoptionManifest(s)); err != nil {
			return nil, err
		}
	}
	lock, err := plannedLock(ctx, s, manifest)
	if err != nil {
		return nil, err
	}
	return resolvePlannedPolicy(ctx, s, manifest, lock)
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
	return config.RenderManifest(manifest)
}

// observeAdoptionInput observes the repository file name, confined to the repository
// (repoFile), through contextopt.ObserveSnapshot: absent is (nil, false, nil), and a failed
// read names the file.
func observeAdoptionInput(ctx context.Context, s *adoptSession, name string) ([]byte, bool, error) {
	path, err := repoFile(s.repoPath, name)
	if err != nil {
		return nil, false, err
	}
	data, exists, err := contextopt.ObserveSnapshot(ctx, path)
	if err != nil {
		return nil, false, fmt.Errorf("inspect %s: %w", name, err)
	}
	return data, exists, nil
}
