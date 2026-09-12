package config

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode"
)

const (
	// AuditMaxFuncLOC preserves the existing audit scanner ceiling during migration.
	AuditMaxFuncLOC    = 60
	maxPolicyLayers    = 2*maxLockEntries + 8
	maxEvidenceSources = 16
)

// ComplexityOverride distinguishes an omitted limit from an invalid explicit zero.
type ComplexityOverride struct {
	MaxCyclomatic *int `yaml:"max_cyclomatic,omitempty" json:"max_cyclomatic,omitempty"`
	MaxCognitive  *int `yaml:"max_cognitive,omitempty" json:"max_cognitive,omitempty"`
	MaxFuncLOC    *int `yaml:"max_func_loc,omitempty" json:"max_func_loc,omitempty"`
	MaxStatements *int `yaml:"max_statements,omitempty" json:"max_statements,omitempty"`
}

// PolicySource identifies exact input bytes. Path is diagnostic, excluded from the
// effective digest so mounting identical configuration elsewhere retains its identity.
type PolicySource struct {
	ID     string `json:"id"`
	Path   string `json:"path,omitempty"`
	SHA256 string `json:"sha256"`
}

// PolicyLayer contributes complexity constraints; every explicit limit must be positive.
type PolicyLayer struct {
	Source     PolicySource
	Complexity ComplexityOverride
}

// EffectivePolicy is an independently owned snapshot, immutable by convention.
// Fields records all sources imposing each effective complexity limit, including ties.
// Only complexity is layered in this slice. Other Policy fields retain defaults and
// repository overrides; they do not claim fleet or profile policy support.
type EffectivePolicy struct {
	Manifest         *Manifest           `json:"-"`
	Policy           ResolvedPolicy      `json:"policy"`
	Sources          []PolicySource      `json:"sources"`
	Fields           map[string][]string `json:"fields"`
	SHA256           string              `json:"sha256"`
	CatalogArtifacts []PolicyArtifact    `json:"-"`
}

// PolicyArtifact is an exact validated archetype snapshot for repository
// bootstrap. External fleet, deployment and workstation files never appear here.
type PolicyArtifact struct {
	RelativePath string
	SHA256       string
	Content      []byte
}

// DevContainerFeature is a selected catalog feature and its JSON-compatible options.
// It is decoded only from CatalogArtifacts retained by the effective-policy loader.
type DevContainerFeature struct {
	Ref     string
	Options map[string]interface{}
}

// Evidence is the compact shared CLI/MCP audit description. Full source hashes
// and paths remain available on Sources for callers retaining structured evidence.
func (p *EffectivePolicy) Evidence() string {
	if p == nil {
		return "effective complexity policy unavailable"
	}
	var evidence strings.Builder
	fmt.Fprintf(&evidence, "Effective complexity policy sha256:%s; max_func_loc=%d; contributors=%s; sources=%d",
		p.SHA256, p.Policy.Complexity.MaxFuncLOC, evidenceContributors(p.Fields["max_func_loc"]), len(p.Sources))
	for i := 0; i < len(p.Sources) && i < maxEvidenceSources; i++ {
		source := p.Sources[i]
		fmt.Fprintf(&evidence, "\n  source=%s sha256:%s", evidenceSourceID(source.ID), source.SHA256)
	}
	if len(p.Sources) > maxEvidenceSources {
		fmt.Fprintf(&evidence, "\n  %d additional sources retained in structured policy provenance", len(p.Sources)-maxEvidenceSources)
	}
	return evidence.String()
}

func evidenceContributors(ids []string) string {
	parts := make([]string, 0, min(len(ids), maxEvidenceSources)+1)
	for i := 0; i < len(ids) && i < maxEvidenceSources; i++ {
		parts = append(parts, evidenceSourceID(ids[i]))
	}
	if len(ids) > maxEvidenceSources {
		parts = append(parts, fmt.Sprintf("+%d", len(ids)-maxEvidenceSources))
	}
	return strings.Join(parts, ",")
}

func evidenceSourceID(id string) string {
	runes := []rune(id)
	if len(runes) > 96 {
		return string(runes[:93]) + "..."
	}
	return id
}

// ResolvePolicy resolves bounded, already decoded layers without filesystem access.
// Every layer can tighten governance; source order is retained for reproducibility.
func ResolvePolicy(ctx context.Context, layers []PolicyLayer) (*EffectivePolicy, error) {
	if ctx == nil || len(layers) > maxPolicyLayers {
		return nil, errors.New("policy requires context and bounded layers")
	}
	result := newEffectivePolicy()
	seen := map[string]bool{result.Sources[0].ID: true}
	for i := 0; i < len(layers) && i < maxPolicyLayers; i++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := result.applyLayer(layers[i], seen); err != nil {
			return nil, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := result.seal(); err != nil {
		return nil, err
	}
	return result, nil
}

func newEffectivePolicy() *EffectivePolicy {
	defaults := DefaultPolicy()
	c := defaults.Complexity
	encoded := fmt.Sprintf("complexity:%d,%d,%d,%d", c.MaxCyclomatic, c.MaxCognitive, c.MaxFuncLOC, c.MaxStatements)
	source := PolicySource{ID: "builtin:defaults-v1", SHA256: policyDigest([]byte(encoded))}
	fields := make(map[string][]string, 4)
	for _, name := range complexityNames() {
		fields[name] = []string{source.ID}
	}
	return &EffectivePolicy{Policy: *defaults, Sources: []PolicySource{source}, Fields: fields}
}

func (p *EffectivePolicy) applyLayer(layer PolicyLayer, seen map[string]bool) error {
	if layer.Source.ID == "" || len(layer.Source.ID) > 512 || seen[layer.Source.ID] || strings.ContainsFunc(layer.Source.ID, unicode.IsControl) {
		return errors.New("policy source IDs must be nonempty, bounded and unique")
	}
	if len(layer.Source.SHA256) != 64 || len(layer.Source.Path) > 4096 {
		return errors.New("policy source digest or path exceeds its bound")
	}
	digest, err := hex.DecodeString(layer.Source.SHA256)
	if err != nil || len(digest) != sha256.Size {
		return fmt.Errorf("policy source %q requires a SHA-256 digest", layer.Source.ID)
	}
	values := layer.Complexity.values()
	current := p.Policy.Complexity.pointers()
	for i, name := range complexityNames() {
		if err := p.applyLimit(name, layer.Source.ID, current[i], values[i]); err != nil {
			return err
		}
	}
	p.Policy = *Join(&p.Policy, layer.Complexity.policy())
	p.Sources = append(p.Sources, layer.Source)
	seen[layer.Source.ID] = true
	return nil
}

func (p *EffectivePolicy) applyLimit(name, source string, current, override *int) error {
	if override == nil {
		return nil
	}
	if *override <= 0 {
		return fmt.Errorf("%s: %s must be positive", source, name)
	}
	if *override < *current {
		p.Fields[name] = []string{source}
	} else if *override == *current {
		p.Fields[name] = append(p.Fields[name], source)
	}
	return nil
}

func complexityNames() [4]string {
	return [4]string{"max_cyclomatic", "max_cognitive", "max_func_loc", "max_statements"}
}

func (p ComplexityOverride) values() [4]*int {
	return [4]*int{p.MaxCyclomatic, p.MaxCognitive, p.MaxFuncLOC, p.MaxStatements}
}

func (p ComplexityOverride) policy() *ResolvedPolicy {
	result := &ResolvedPolicy{}
	targets := result.Complexity.pointers()
	for i, value := range p.values() {
		if value != nil {
			*targets[i] = *value
		}
	}
	return result
}

func (p *ComplexityPolicy) pointers() [4]*int {
	return [4]*int{&p.MaxCyclomatic, &p.MaxCognitive, &p.MaxFuncLOC, &p.MaxStatements}
}

func (p *EffectivePolicy) seal() error {
	sources := append([]PolicySource(nil), p.Sources...)
	for i := 0; i < len(sources) && i <= maxPolicyLayers; i++ {
		sources[i].Path = ""
	}
	encoded, err := json.Marshal(struct {
		Policy  ResolvedPolicy
		Sources []PolicySource
		Fields  map[string][]string
	}{p.Policy, sources, p.Fields})
	if err != nil {
		return fmt.Errorf("encode effective policy: %w", err)
	}
	p.SHA256 = policyDigest(encoded)
	return nil
}

func policyDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
