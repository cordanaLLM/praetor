package config

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"
	"unicode/utf8"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"gopkg.in/yaml.v3"
)

const maxPolicyBytes = 8 << 20

// EffectiveOptions selects explicit sources without consulting the process home,
// environment, network, or implicit user configuration. CatalogRoot contains
// .config/archetypes. External documents contribute their root complexity mapping.
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
	layers, err := loader.pinnedLayers(paths, manifest)
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
	return node, PolicyLayer{Source: PolicySource{ID: id, Path: path, SHA256: policyDigest(data)}}, nil
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
	overrides := policyMember(node, "overrides")
	if overrides != nil && overrides.Kind != yaml.MappingNode {
		return nil, layer, errors.New("manifest overrides must be a mapping")
	}
	layer.Complexity, err = decodeComplexity(policyMember(overrides, "complexity"))
	return &manifest, layer, err
}

func (l *effectiveLoader) externalLayers(opts EffectiveOptions, layers []PolicyLayer) ([]PolicyLayer, error) {
	paths := []struct{ id, path string }{
		{"fleet", opts.FleetPath}, {"organization", opts.OrganizationPath},
		{"deployment", opts.DeploymentPath}, {"workstation", opts.WorkstationPath},
	}
	for _, source := range paths {
		if source.path == "" {
			continue
		}
		node, layer, err := l.document(source.path, source.id)
		if err != nil {
			return nil, err
		}
		complexity := policyMember(node, "complexity")
		if complexity == nil {
			return nil, fmt.Errorf("%s policy requires an explicit complexity mapping", source.id)
		}
		layer.Complexity, err = decodeComplexity(complexity)
		if err != nil {
			return nil, fmt.Errorf("%s policy: %w", source.id, err)
		}
		layers = append(layers, layer)
	}
	return layers, nil
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
	// Preserve current non-complexity behavior until those controls have their own
	// schema and consumer migration. No external control is silently implied.
	result.Policy.ApplyOverrides(Overrides{
		BranchProtection: manifest.Overrides.BranchProtection,
		SupplyChain:      manifest.Overrides.SupplyChain,
	})
	if err := result.seal(); err != nil {
		return nil, err
	}
	return result, nil
}
