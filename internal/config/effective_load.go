package config

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/util"
	"gopkg.in/yaml.v3"
)

const maxPolicyBytes = 8 << 20

// EffectiveOptions selects explicit sources without consulting the process home,
// environment, network, or implicit user configuration. CatalogRoot contains
// .config/archetypes. External documents contribute their root complexity mapping and
// their operator sections (operatorSectionNames).
// Omitted paths add no layer; explicitly selected missing files are errors.
// Callers enforce authorization/confinement for these explicit paths. Root selects
// the lockfile location, while ManifestPath can deliberately select another file.
type EffectiveOptions struct {
	Root             string
	ManifestPath     string
	CatalogRoot      string
	FleetPath        string
	OrganizationPath string
	DeploymentPath   string
	WorkstationPath  string
	Audit            bool
	// unpinned skips the lock and its pinned profiles and facets, for a repository that has
	// no .standards.lock yet (LoadUnadoptedEffectivePolicyContext).
	unpinned bool
}

// LoadEffectivePolicyContext resolves defaults, pinned profiles/facets, explicit
// fleet/org/deployment/workstation sources, repository overrides, and the optional
// audit compatibility ceiling. All policy sources are local bounded snapshots.
func LoadEffectivePolicyContext(ctx context.Context, opts EffectiveOptions) (*EffectivePolicy, error) {
	return loadEffectivePolicy(ctx, opts, nil)
}

// LoadEffectivePolicyInputsContext resolves planned manifest and lock bytes using
// the same loader as disk-backed audits. Only these two named inputs are overlaid;
// catalog and external policy sources are still read from their selected paths.
func LoadEffectivePolicyInputsContext(ctx context.Context, opts EffectiveOptions, manifestBytes, lockBytes []byte) (*EffectivePolicy, error) {
	if len(manifestBytes) > contextopt.MaxSourceBytes || len(lockBytes) > contextopt.MaxSourceBytes {
		return nil, errors.New("planned manifest and lock each require at most 1 MiB")
	}
	inputs := map[string][]byte{
		"repository": append([]byte(nil), manifestBytes...),
		"lock":       append([]byte(nil), lockBytes...),
	}
	return loadEffectivePolicy(ctx, opts, inputs)
}

// LoadUnadoptedEffectivePolicyContext resolves the policy for a command that also runs in a
// repository before adoption, such as `praetorctl state status`. A root without
// .standards.yaml has no policy: it returns nil and a notice saying so. A manifest without
// .standards.lock resolves without pinned profiles or facets and returns NoLockNotice, the
// case ResolveRepositoryPolicy handles the same way. Every other case is
// LoadEffectivePolicyContext, so an unreadable or mismatched lock is an error, never skipped.
func LoadUnadoptedEffectivePolicyContext(ctx context.Context, opts EffectiveOptions) (*EffectivePolicy, string, error) {
	if ctx == nil || opts.Root == "" {
		return nil, "", errors.New("effective policy requires context and explicit repository root")
	}
	paths, err := normalizeEffectiveOptions(opts)
	if err != nil {
		return nil, "", err
	}
	if !util.PathExists(paths.ManifestPath) {
		return nil, "no " + ManifestFileName + ": no repository policy applies", nil
	}
	notice := ""
	if !util.PathExists(filepath.Join(paths.Root, LockFileName)) {
		opts.unpinned, notice = true, noLockNotice(opts)
	}
	policy, err := loadEffectivePolicy(ctx, opts, nil)
	return policy, notice, err
}

// noLockNotice is NoLockNotice for a resolution without external documents. An explicitly
// selected fleet, organization, deployment or workstation document still applies without a
// lock, so the notice then names each one instead of claiming defaults and overrides only.
func noLockNotice(opts EffectiveOptions) string {
	sources := externalSources(opts)
	if len(sources) == 0 {
		return NoLockNotice
	}
	ids := make([]string, 0, len(sources))
	for _, source := range sources {
		ids = append(ids, source.id)
	}
	return fmt.Sprintf("no %s: built-in defaults, repository overrides and the %s policy only; no pinned profile or facet applies",
		LockFileName, strings.Join(ids, ", "))
}

// DeclaresBacklogContext reports whether a layer the effective policy reads for opts declares
// a backlog section: the repository manifest, an explicitly selected external document or,
// when the repository carries a lock, a profile or facet the manifest selects, read from the
// catalog without the lock check. A read-only view uses it to tell a policy that caps nothing
// from one whose caps do not resolve. A source it cannot read is an error, never a layer that
// declares nothing; a root without a manifest declares nothing.
func DeclaresBacklogContext(ctx context.Context, opts EffectiveOptions) (bool, error) {
	if ctx == nil || opts.Root == "" {
		return false, errors.New("backlog declaration probe requires context and explicit repository root")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	paths, err := normalizeEffectiveOptions(opts)
	if err != nil || !util.PathExists(paths.ManifestPath) {
		return false, err
	}
	loader := effectiveLoader{ctx: ctx}
	manifest, _, err := loader.manifest(paths.ManifestPath)
	if err != nil || manifest.Backlog != nil {
		return err == nil, err
	}
	declared, err := loader.externalDeclaresBacklog(paths)
	if err != nil || declared || !util.PathExists(filepath.Join(paths.Root, LockFileName)) {
		return declared, err
	}
	return loader.catalogDeclaresBacklog(paths.CatalogRoot, manifest)
}

// externalDeclaresBacklog reports whether an explicitly selected external document carries a
// backlog section. A selected document that cannot be read is an error.
func (l *effectiveLoader) externalDeclaresBacklog(opts EffectiveOptions) (bool, error) {
	for _, source := range externalSources(opts) {
		node, _, err := l.document(source.path, source.id)
		if err != nil || policyMember(node, "backlog") != nil {
			return err == nil, err
		}
	}
	return false, nil
}

// catalogDeclaresBacklog reports whether a profile or facet the manifest selects declares
// backlog caps in the catalog under root. A selected id the catalog does not hold is an error.
func (l *effectiveLoader) catalogDeclaresBacklog(root string, manifest *Manifest) (bool, error) {
	if len(manifest.Profiles)+len(manifest.Facets) == 0 {
		return false, nil
	}
	profiles, facets, err := archetypeSources(l.ctx, root)
	if err != nil {
		return false, err
	}
	declared, err := l.archetypesDeclareBacklog("profile", manifest.Profiles, profiles, root)
	if err != nil || declared {
		return declared, err
	}
	return l.archetypesDeclareBacklog("facet", manifest.Facets, facets, root)
}

// archetypesDeclareBacklog reads each selected catalog file of one kind and reports whether
// one declares backlog caps.
func (l *effectiveLoader) archetypesDeclareBacklog(kind string, ids []string, index map[string]string, root string) (bool, error) {
	for i := 0; i < len(ids) && i < maxLockEntries; i++ {
		path, ok := index[ids[i]]
		if !ok {
			return false, fmt.Errorf("%s %q is not in the catalog at %s, so its backlog caps cannot be read", kind, ids[i], root)
		}
		data, err := l.snapshot(path)
		if err != nil {
			return false, err
		}
		archetype, err := decodeArchetype(l.ctx, path, data)
		if err != nil || archetype.Backlog != (BacklogCaps{}) {
			return err == nil, err
		}
	}
	return false, nil
}

func loadEffectivePolicy(ctx context.Context, opts EffectiveOptions, inputs map[string][]byte) (*EffectivePolicy, error) {
	if ctx == nil || opts.Root == "" {
		return nil, errors.New("effective policy requires context and explicit repository root")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	paths, err := normalizeEffectiveOptions(opts)
	if err != nil {
		return nil, err
	}
	loader := effectiveLoader{ctx: ctx, inputs: inputs}
	manifest, repository, err := loader.manifest(paths.ManifestPath)
	if err != nil {
		return nil, err
	}
	var layers []PolicyLayer
	if !paths.unpinned {
		layers, err = loader.pinnedLayers(paths, manifest)
	}
	if err != nil {
		return nil, err
	}
	layers, err = loader.externalLayers(paths, layers)
	if err != nil {
		return nil, err
	}
	layers = append(layers, repository)
	if paths.Audit {
		layers = append(layers, auditCompatibilityLayer())
	}
	result, err := finishEffectivePolicy(ctx, manifest, layers)
	if err != nil {
		return nil, err
	}
	result.CatalogArtifacts = loader.artifacts
	return result, nil
}

func normalizeEffectiveOptions(opts EffectiveOptions) (EffectiveOptions, error) {
	root, err := filepath.Abs(opts.Root)
	if err != nil {
		return opts, err
	}
	opts.Root = root
	if opts.ManifestPath == "" {
		opts.ManifestPath = filepath.Join(root, ".standards.yaml")
	}
	opts.ManifestPath, err = filepath.Abs(opts.ManifestPath)
	if err != nil {
		return opts, err
	}
	if opts.CatalogRoot == "" {
		opts.CatalogRoot = root
	}
	opts.CatalogRoot, err = filepath.Abs(opts.CatalogRoot)
	if err != nil {
		return opts, err
	}
	return opts, nil
}

type effectiveLoader struct {
	ctx       context.Context
	bytes     int
	artifacts []PolicyArtifact
	inputs    map[string][]byte
}

func (l *effectiveLoader) snapshot(path string) ([]byte, error) {
	data, err := contextopt.ReadSnapshot(l.ctx, path)
	if err != nil {
		return nil, fmt.Errorf("read policy source %s: %w", path, err)
	}
	return l.countSnapshot(data)
}

func (l *effectiveLoader) sourceSnapshot(path, id string) ([]byte, error) {
	if data, ok := l.inputs[id]; ok {
		return l.countSnapshot(data)
	}
	return l.snapshot(path)
}

func (l *effectiveLoader) countSnapshot(data []byte) ([]byte, error) {
	if err := l.ctx.Err(); err != nil {
		return nil, err
	}
	if len(data) > contextopt.MaxSourceBytes || !utf8.Valid(data) {
		return nil, errors.New("policy source must be UTF-8 and at most 1 MiB")
	}
	l.bytes += len(data)
	if l.bytes > maxPolicyBytes {
		return nil, errors.New("effective policy sources exceed aggregate byte bound")
	}
	return data, nil
}

func (l *effectiveLoader) document(path, id string) (*yaml.Node, PolicyLayer, error) {
	data, err := l.sourceSnapshot(path, id)
	if err != nil {
		return nil, PolicyLayer{}, err
	}
	node, err := decodePolicyDocument(l.ctx, data)
	if err != nil {
		return nil, PolicyLayer{}, fmt.Errorf("policy source %s: %w", id, err)
	}
	return node, PolicyLayer{Source: PolicySource{ID: id, Path: path, SHA256: sourceDigest(data)}}, nil
}

func (l *effectiveLoader) manifest(path string) (*Manifest, PolicyLayer, error) {
	node, layer, err := l.document(path, "repository")
	if err != nil {
		return nil, layer, err
	}
	var manifest Manifest
	if err := node.Decode(&manifest); err != nil {
		return nil, layer, fmt.Errorf("decode policy manifest: %w", err)
	}
	if manifest.Version != 1 {
		return nil, layer, errors.New("effective policy manifest requires version 1")
	}
	if err := validateManifestReviewPolicy(&manifest); err != nil {
		return nil, layer, fmt.Errorf("effective policy manifest: %w", err)
	}
	// The audit reads overrides.actions and workflow_runs from this manifest, so it holds them
	// to LoadManifest's rules rather than comparing a malformed declaration with the forge.
	if err := validateManifestActions(&manifest); err != nil {
		return nil, layer, fmt.Errorf("effective policy manifest: %w", err)
	}
	overrides := policyMember(node, "overrides")
	if overrides != nil && overrides.Kind != yaml.MappingNode {
		return nil, layer, errors.New("manifest overrides must be a mapping")
	}
	if manifest.Backlog != nil {
		layer.Backlog = manifest.Backlog.Caps
	}
	layer.Complexity, err = decodeComplexity(policyMember(overrides, "complexity"))
	return &manifest, layer, err
}

// externalSource is one explicitly selected external policy document and the layer id it
// resolves under.
type externalSource struct{ id, path string }

// externalSources lists the external documents opts selects, in resolution order. An omitted
// path selects nothing.
func externalSources(opts EffectiveOptions) []externalSource {
	all := [...]externalSource{
		{"fleet", opts.FleetPath}, {"organization", opts.OrganizationPath},
		{"deployment", opts.DeploymentPath}, {"workstation", opts.WorkstationPath},
	}
	selected := make([]externalSource, 0, len(all))
	for _, source := range all {
		if source.path != "" {
			selected = append(selected, source)
		}
	}
	return selected
}

func (l *effectiveLoader) externalLayers(opts EffectiveOptions, layers []PolicyLayer) ([]PolicyLayer, error) {
	for _, source := range externalSources(opts) {
		layer, err := l.externalLayer(source.id, source.path)
		if err != nil {
			return nil, err
		}
		layers = append(layers, layer)
	}
	return layers, nil
}

// externalLayer decodes one explicitly selected document. It must carry at least one owned
// section: complexity, backlog or an operator section (operatorSectionNames). Other root keys
// stay tolerated.
func (l *effectiveLoader) externalLayer(id, path string) (PolicyLayer, error) {
	node, layer, err := l.document(path, id)
	if err != nil {
		return layer, err
	}
	var owned bool
	layer.Settings, owned, err = decodeOperatorSections(node)
	if err != nil {
		return layer, fmt.Errorf("%s policy: %w", id, err)
	}
	complexity, backlog := policyMember(node, "complexity"), policyMember(node, "backlog")
	if complexity == nil && backlog == nil && !owned {
		return layer, fmt.Errorf("%s policy requires a complexity or backlog section or one of the operator sections %s",
			id, strings.Join(operatorSectionNames[:], ", "))
	}
	layer.Complexity, err = decodeComplexity(complexity)
	if err == nil {
		layer.Backlog, err = decodeBacklog(backlog)
	}
	if err != nil {
		return layer, fmt.Errorf("%s policy: %w", id, err)
	}
	return layer, nil
}

func auditCompatibilityLayer() PolicyLayer {
	limit := AuditMaxFuncLOC
	return PolicyLayer{
		Source:     PolicySource{ID: "builtin:audit-compat-v1", SHA256: policyDigest([]byte(fmt.Sprintf("max_func_loc=%d", limit)))},
		Complexity: ComplexityOverride{MaxFuncLOC: &limit},
	}
}

func finishEffectivePolicy(ctx context.Context, manifest *Manifest, layers []PolicyLayer) (*EffectivePolicy, error) {
	result, err := ResolvePolicy(ctx, layers)
	if err != nil {
		return nil, err
	}
	result.Manifest = manifest
	// The pinned profiles and facets already joined their controls in ResolvePolicy; the
	// repository's branch protection and supply chain overrides apply after that join, so
	// they only tighten it (review_mode: single_maintainer is the one explicit relaxation).
	// External layers carry no controls, so none is silently implied.
	result.Policy.ApplyOverrides(Overrides{
		BranchProtection: manifest.Overrides.BranchProtection,
		SupplyChain:      manifest.Overrides.SupplyChain,
	})
	if err := result.seal(); err != nil {
		return nil, err
	}
	return result, nil
}
