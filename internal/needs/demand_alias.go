package needs

import (
	"encoding/json"
	"fmt"

	"gopkg.in/yaml.v3"
)

// legacyReplacementKey is the former serialized name of framework_replacement (ADR-0014
// §5). Decoding reads it as a deprecated alias when the new key is empty; writing uses the
// new key only. The alias is removed two minor releases after ADR-0014 is accepted.
const legacyReplacementKey = "golusoris_replacement"

// legacyReplacementDeprecation is the warning a row read through the alias carries.
const legacyReplacementDeprecation = "the .needs.yaml key " + legacyReplacementKey +
	" is deprecated; it was read as framework_replacement, and the next `needs scan --write` writes the new key"

// demandFields is DependencyDemand without its decoding methods, so decoding it uses the
// struct tags of every field and a field added later can never be skipped.
type demandFields DependencyDemand

// demandWire is the decoded form of a DependencyDemand: every current field plus the alias.
type demandWire struct {
	demandFields      `yaml:",inline"`
	LegacyReplacement string `json:"golusoris_replacement,omitempty" yaml:"golusoris_replacement,omitempty"`
}

// UnmarshalYAML decodes a demand, reading the deprecated replacement key as an alias.
func (d *DependencyDemand) UnmarshalYAML(node *yaml.Node) error {
	var wire demandWire
	if err := node.Decode(&wire); err != nil {
		return fmt.Errorf("decode dependency demand: %w", err)
	}
	return d.adopt(wire)
}

// UnmarshalJSON decodes a demand, reading the deprecated replacement key as an alias.
func (d *DependencyDemand) UnmarshalJSON(data []byte) error {
	var wire demandWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return fmt.Errorf("decode dependency demand: %w", err)
	}
	return d.adopt(wire)
}

// adopt stores a decoded demand. A demand naming two different replacements under the new
// key and the alias is an error: neither can be chosen without guessing.
func (d *DependencyDemand) adopt(wire demandWire) error {
	*d = DependencyDemand(wire.demandFields)
	if wire.LegacyReplacement == "" {
		return nil
	}
	if d.FrameworkReplacement != "" && d.FrameworkReplacement != wire.LegacyReplacement {
		return fmt.Errorf("dependency %q sets framework_replacement %q and the deprecated %s %q",
			d.Package, d.FrameworkReplacement, legacyReplacementKey, wire.LegacyReplacement)
	}
	d.FrameworkReplacement, d.legacyKey = wire.LegacyReplacement, true
	return nil
}

// repoNeedsFields is RepoNeeds without its decoding methods; see demandFields.
type repoNeedsFields RepoNeeds

// UnmarshalYAML decodes a row, refuses an invalid non_goals list (validateNonGoals) and
// records a deprecation when a demand used the alias.
func (r *RepoNeeds) UnmarshalYAML(node *yaml.Node) error {
	var fields repoNeedsFields
	if err := node.Decode(&fields); err != nil {
		return fmt.Errorf("decode repository needs: %w", err)
	}
	if err := validateNonGoals((*RepoNeeds)(&fields)); err != nil {
		return err
	}
	*r = RepoNeeds(fields)
	r.recordLegacyKeys()
	return nil
}

// UnmarshalJSON decodes a row, refuses an invalid non_goals list (validateNonGoals) and
// records a deprecation when a demand used the alias.
func (r *RepoNeeds) UnmarshalJSON(data []byte) error {
	var fields repoNeedsFields
	if err := json.Unmarshal(data, &fields); err != nil {
		return fmt.Errorf("decode repository needs: %w", err)
	}
	if err := validateNonGoals((*RepoNeeds)(&fields)); err != nil {
		return err
	}
	*r = RepoNeeds(fields)
	r.recordLegacyKeys()
	return nil
}

// recordLegacyKeys adds legacyReplacementDeprecation once when any demand was read through
// the alias.
func (r *RepoNeeds) recordLegacyKeys() {
	for _, demands := range [...][]DependencyDemand{r.Dependencies, r.StandardLibraryImports} {
		for _, demand := range demands {
			if demand.legacyKey {
				r.Deprecations = appendUniqueStr(r.Deprecations, legacyReplacementDeprecation)
				return
			}
		}
	}
}
