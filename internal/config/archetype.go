package config

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
	"gopkg.in/yaml.v3"
)

// Archetype is the one decoded form of a profile or facet catalog file: its identity,
// its descriptive header and every lattice dimension it contributes. The catalog index,
// the effective-policy loader and DevContainer feature resolution all read catalog files
// through decodeArchetype, so the schema has exactly one reader.
type Archetype struct {
	ID          string
	Name        string
	Description string
	Runtime     string
	Complexity  ComplexityOverride
	Controls    ArchetypeControls
}

// ArchetypeControls are the lattice dimensions an archetype contributes besides
// complexity, which keeps its override form because each limit carries field provenance.
// The zero value contributes nothing: it is the identity of Join.
type ArchetypeControls struct {
	BranchProtection BranchProtectionPolicy
	SupplyChain      SupplyChainPolicy
	Memory           MemoryPolicy
	ErrorUnwraps     ErrorUnwrapMode
	Linters          []string
	DevFeatures      []DevContainerFeature
}

// archetypeDocument is the closed schema of one catalog file. It is decoded with unknown
// fields refused, so a misspelled or unsupported key fails the file instead of silently
// contributing nothing while the archetype reads as configured.
type archetypeDocument struct {
	ID                   string                 `yaml:"id"`
	Name                 string                 `yaml:"name"`
	Description          string                 `yaml:"description"`
	Runtime              string                 `yaml:"runtime"`
	Complexity           yaml.Node              `yaml:"complexity"`
	Memory               MemoryPolicy           `yaml:"memory"`
	ErrorUnwraps         ErrorUnwrapMode        `yaml:"error_unwraps"`
	BranchProtection     BranchProtectionPolicy `yaml:"branch_protection"`
	SupplyChain          SupplyChainPolicy      `yaml:"supply_chain"`
	Linters              []string               `yaml:"linters"`
	DevContainerFeatures yaml.Node              `yaml:"devcontainer_features"`
}

// decodeArchetype checks the bounded document shape every policy source shares, then
// decodes the closed archetype schema. A missing or blank id defaults to the file stem.
func decodeArchetype(ctx context.Context, path string, data []byte) (Archetype, error) {
	if _, err := decodePolicyDocument(ctx, data); err != nil {
		return Archetype{}, fmt.Errorf("parse archetype %s: %w", path, err)
	}
	var doc archetypeDocument
	if err := util.DecodeYAMLStrict(data, &doc); err != nil {
		return Archetype{}, fmt.Errorf("parse archetype %s: %w", path, err)
	}
	archetype, err := doc.archetype(path)
	if err != nil {
		return Archetype{}, fmt.Errorf("archetype %s: %w", path, err)
	}
	return archetype, nil
}

func (d *archetypeDocument) archetype(path string) (Archetype, error) {
	id := strings.TrimSpace(d.ID)
	if id == "" {
		id = strings.TrimSuffix(filepath.Base(path), ".yaml")
	}
	complexity, err := decodeComplexityLimits(declaredNode(&d.Complexity), true)
	if err != nil {
		return Archetype{}, err
	}
	features, err := decodeFeatureSequence(declaredNode(&d.DevContainerFeatures))
	if err != nil {
		return Archetype{}, err
	}
	controls := ArchetypeControls{
		BranchProtection: d.BranchProtection, SupplyChain: d.SupplyChain, Memory: d.Memory,
		ErrorUnwraps: d.ErrorUnwraps, Linters: d.Linters, DevFeatures: features,
	}
	if err := controls.validate(); err != nil {
		return Archetype{}, err
	}
	return Archetype{
		ID: id, Name: d.Name, Description: d.Description, Runtime: d.Runtime,
		Complexity: complexity, Controls: controls,
	}, nil
}

// declaredNode returns nil for a key the document omitted, which decodes to a zero Node.
func declaredNode(node *yaml.Node) *yaml.Node {
	if node.Kind == 0 {
		return nil
	}
	return node
}

// validate holds decoded controls and those a ResolvePolicy caller builds directly to
// one contract: review_mode is a repository-only relaxation no catalog layer may set,
// counts cannot be negative, and names stay within the bounds the digest verifies.
func (c ArchetypeControls) validate() error {
	if c.BranchProtection.ReviewMode != "" {
		return errors.New("branch_protection.review_mode is a repository-only setting")
	}
	if c.BranchProtection.RequiredApprovingReviewers < 0 || c.SupplyChain.SLSALevel < 0 {
		return errors.New("required_approving_reviewers and slsa_level cannot be negative")
	}
	if !c.ErrorUnwraps.known() {
		return fmt.Errorf("unsupported error_unwraps mode %q", c.ErrorUnwraps)
	}
	if len(c.DevFeatures) > maxDevContainerFeatures {
		return errors.New("devcontainer_features exceeds bounds")
	}
	if err := validatePolicyNames(c.Linters); err != nil {
		return fmt.Errorf("linters: %w", err)
	}
	return nil
}

// contribution is the controls' share of a policy layer, joined after complexity.
func (c ArchetypeControls) contribution(result *ResolvedPolicy) {
	result.BranchProtection = c.BranchProtection
	result.SupplyChain = c.SupplyChain
	result.Memory = c.Memory
	result.ErrorUnwraps = c.ErrorUnwraps
	result.Linters = append([]string(nil), c.Linters...)
	refs := make([]string, 0, len(c.DevFeatures))
	for i := 0; i < len(c.DevFeatures) && i < maxDevContainerFeatures; i++ {
		refs = append(refs, c.DevFeatures[i].Ref)
	}
	result.DevFeatures = refs
}
