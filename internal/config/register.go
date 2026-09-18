package config

import (
	"errors"
	"fmt"
	"sort"

	"github.com/cordanaLLM/praetor/internal/router"
	"gopkg.in/yaml.v3"
)

// TextRegister names the voice a text is written in. The audience decides it, never a
// global switch: a maintainer on the forge, a reader of the documentation and an agent
// that pays for every token again on the next hop need three different texts.
type TextRegister string

const (
	// TextRegisterSocial is scannable human prose for the forge.
	TextRegisterSocial TextRegister = "social"
	// TextRegisterDocs is complete, reference-grade prose for documentation.
	TextRegisterDocs TextRegister = "docs"
	// TextRegisterInternal is caveman text for agent-to-agent traffic; the value stays
	// "internal" and the `caveman` skill (.agents/skills/caveman) carries its form.
	TextRegisterInternal TextRegister = "internal"
)

// RegisterSurface names an audience surface of the register policy.
type RegisterSurface string

const (
	// SurfaceForge covers issues, pull-request bodies, review comments and commit bodies.
	SurfaceForge RegisterSurface = "forge"
	// SurfaceDocs covers docs/, README, ADR bodies and notebook documents.
	SurfaceDocs RegisterSurface = "docs"
	// SurfaceAgent covers briefs, fan-out prompts, workflow returns and provider
	// instructions, and is the fallback when a caller names neither surface nor task.
	SurfaceAgent RegisterSurface = "agent"
)

const (
	// EvidenceInlineMaxLinesDefault and EvidenceInlineMaxTokensDefault bound evidence that
	// may travel inline. internal/lockdown aliases them for the SARIF distillation cap, so
	// the two bounds cannot drift apart.
	EvidenceInlineMaxLinesDefault  = 58
	EvidenceInlineMaxTokensDefault = 1500
	// MaxRegisterTaskRows bounds the per-task rows of one manifest.
	MaxRegisterTaskRows = router.MaxRoutingTags
	// RegisterMaxTokensFloor and RegisterMaxTokensCeiling bound a per-task output budget.
	// internal/repairrun validates a provider request against the same two constants, so
	// a budget the manifest accepts is one the provider path accepts.
	RegisterMaxTokensFloor   = 256
	RegisterMaxTokensCeiling = 8192
)

// RegisterTask is one per-task row: the register of the task's own product (its brief and
// return) and an optional output budget. Zero MaxTokens means no budget.
type RegisterTask struct {
	Register  TextRegister `yaml:"register"`
	MaxTokens int          `yaml:"max_tokens,omitempty"`
	// maxTokensSet records an explicit max_tokens key so that a written zero is rejected
	// instead of reading as "no budget".
	maxTokensSet bool
}

// EvidenceBounds is the inline evidence ceiling. Zero fields mean the default.
type EvidenceBounds struct {
	InlineMaxLines  int `yaml:"inline_max_lines,omitempty"`
	InlineMaxTokens int `yaml:"inline_max_tokens,omitempty"`
	// linesSet and tokensSet record explicit keys, for the same reason as maxTokensSet.
	linesSet, tokensSet bool
}

// RegisterPolicy is the register: section of .standards.yaml. It is repository-only and
// deliberately outside ResolvedPolicy, Join and ApplyOverrides: a register is a choice,
// not a bound, so no fleet or profile layer can set it and it never reaches the resolved
// policy.
type RegisterPolicy struct {
	Surfaces map[RegisterSurface]TextRegister `yaml:"surfaces,omitempty"`
	Tasks    map[string]RegisterTask          `yaml:"tasks,omitempty"`
	Evidence EvidenceBounds                   `yaml:"evidence,omitempty"`
}

// Resolution is the register a caller must write in, with the row that decided it.
type Resolution struct {
	Register  TextRegister
	MaxTokens int
	Source    string
}

func knownTextRegister(r TextRegister) bool {
	return r == TextRegisterSocial || r == TextRegisterDocs || r == TextRegisterInternal
}

// UnmarshalYAML rejects unknown, empty and non-string registers at the source boundary.
func (r *TextRegister) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.ScalarNode || node.Tag != "!!str" {
		return errors.New("text register must be a string enum")
	}
	register := TextRegister(node.Value)
	if !knownTextRegister(register) {
		return fmt.Errorf("unsupported text register %q", register)
	}
	*r = register
	return nil
}

// registerIntFields decodes the integer keys of a small mapping into their targets and
// reports which keys were present. A custom unmarshaler receives the raw node, where the
// decoder's KnownFields and duplicate-key checks no longer apply, so both are done here.
// Keys mapped to nil are accepted and left to the caller.
func registerIntFields(node *yaml.Node, what string, targets map[string]*int) (map[string]bool, error) {
	limit := 2 * len(targets)
	if node.Kind != yaml.MappingNode || len(node.Content) > limit {
		return nil, fmt.Errorf("%s must be a mapping of at most %d known keys", what, len(targets))
	}
	present := make(map[string]bool, len(targets))
	for i := 0; i+1 < len(node.Content) && i < limit; i += 2 {
		key, value := node.Content[i].Value, node.Content[i+1]
		target, known := targets[key]
		if !known || present[key] {
			return nil, fmt.Errorf("unknown or duplicated %s field %q", what, key)
		}
		present[key] = true
		if err := decodeRegisterInt(value, what, key, target); err != nil {
			return nil, err
		}
	}
	return present, nil
}

// decodeRegisterInt decodes one integer scalar; a nil target marks a key the caller reads.
func decodeRegisterInt(value *yaml.Node, what, key string, target *int) error {
	if target == nil {
		return nil
	}
	if value.Kind != yaml.ScalarNode || value.Tag != "!!int" {
		return fmt.Errorf("%s %s must be an integer", what, key)
	}
	if err := value.Decode(target); err != nil {
		return fmt.Errorf("%s %s: %w", what, key, err)
	}
	return nil
}

// UnmarshalYAML accepts the scalar shorthand `label: social` and the mapping
// `label: {register: social, max_tokens: 512}`.
func (t *RegisterTask) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		return t.Register.UnmarshalYAML(node)
	}
	present, err := registerIntFields(node, "register task", map[string]*int{"register": nil, "max_tokens": &t.MaxTokens})
	if err != nil {
		return err
	}
	t.maxTokensSet = present["max_tokens"]
	for i := 0; i+1 < len(node.Content) && i < 4; i += 2 {
		if node.Content[i].Value == "register" {
			return t.Register.UnmarshalYAML(node.Content[i+1])
		}
	}
	return nil
}

// UnmarshalYAML records which bounds the manifest wrote, so an explicit zero is an error.
func (e *EvidenceBounds) UnmarshalYAML(node *yaml.Node) error {
	present, err := registerIntFields(node, "register evidence", map[string]*int{
		"inline_max_lines":  &e.InlineMaxLines,
		"inline_max_tokens": &e.InlineMaxTokens,
	})
	if err != nil {
		return err
	}
	e.linesSet, e.tokensSet = present["inline_max_lines"], present["inline_max_tokens"]
	return nil
}

// DefaultRegisterPolicy returns the policy that governs a repository without a register:
// section. Default rows carry a register only: an output budget comes from measured
// dispatch data, so none is shipped as an opinion.
func DefaultRegisterPolicy() RegisterPolicy {
	return RegisterPolicy{
		Surfaces: map[RegisterSurface]TextRegister{
			SurfaceForge: TextRegisterSocial,
			SurfaceDocs:  TextRegisterDocs,
			SurfaceAgent: TextRegisterInternal,
		},
		Tasks: map[string]RegisterTask{
			"architecture_synthesis":   {Register: TextRegisterDocs},
			"function_docstrings":      {Register: TextRegisterDocs},
			"commit_message_synthesis": {Register: TextRegisterSocial},
			"waiver_signoff":           {Register: TextRegisterSocial},
		},
		Evidence: EvidenceBounds{
			InlineMaxLines:  EvidenceInlineMaxLinesDefault,
			InlineMaxTokens: EvidenceInlineMaxTokensDefault,
		},
	}
}

// EffectiveRegister returns the defaults with every set field of the manifest section
// applied: surfaces are replaced key-wise, explicit task rows win over default rows, and
// evidence bounds can only tighten.
func (m *Manifest) EffectiveRegister() RegisterPolicy {
	policy := DefaultRegisterPolicy()
	if m == nil || m.Register == nil {
		return policy
	}
	for surface, register := range m.Register.Surfaces {
		policy.Surfaces[surface] = register
	}
	for label, row := range m.Register.Tasks {
		policy.Tasks[label] = RegisterTask{Register: row.Register, MaxTokens: row.MaxTokens}
	}
	tightenPositive(&policy.Evidence.InlineMaxLines, m.Register.Evidence.InlineMaxLines)
	tightenPositive(&policy.Evidence.InlineMaxTokens, m.Register.Evidence.InlineMaxTokens)
	return policy
}

// Resolve returns the register for one surface and task label. The forge and the docs
// surface own their audience, so a task never changes them; on the agent surface (or when
// no surface is named) the task row wins and surfaces.agent is the fallback.
func (p RegisterPolicy) Resolve(surface RegisterSurface, task string) Resolution {
	if surface == SurfaceForge || surface == SurfaceDocs {
		return Resolution{Register: p.surfaceRegister(surface), Source: "surfaces." + string(surface)}
	}
	if row, ok := p.Tasks[task]; ok && knownTextRegister(row.Register) {
		return Resolution{Register: row.Register, MaxTokens: row.MaxTokens, Source: "tasks." + task}
	}
	return Resolution{Register: p.surfaceRegister(SurfaceAgent), Source: "surfaces." + string(SurfaceAgent)}
}

// surfaceRegister falls back to the default so that a partially filled policy still
// resolves to a usable register.
func (p RegisterPolicy) surfaceRegister(surface RegisterSurface) TextRegister {
	if register, ok := p.Surfaces[surface]; ok && knownTextRegister(register) {
		return register
	}
	return DefaultRegisterPolicy().Surfaces[surface]
}

// ValidateTaskLabels fails when a task row is keyed by a label the router does not
// declare. One label set drives the cost tier and the text register; a second vocabulary
// would be two implementations of one behaviour.
func (p RegisterPolicy) ValidateTaskLabels(known []string) error {
	declared := make(map[string]bool, len(known))
	for _, label := range known {
		declared[label] = true
	}
	for _, label := range p.sortedTaskLabels() {
		if !declared[label] {
			return fmt.Errorf("register task %q is not a declared target_tasks label", label)
		}
	}
	return nil
}

func (p RegisterPolicy) sortedTaskLabels() []string {
	labels := make([]string, 0, len(p.Tasks))
	for label := range p.Tasks {
		labels = append(labels, label)
	}
	sort.Strings(labels)
	return labels
}

// validateManifestRegister checks the section LoadManifest just decoded. Labels are
// checked against the routing vocabulary where routing is loaded, not here.
func validateManifestRegister(m *Manifest) error {
	if m == nil || m.Register == nil {
		return nil
	}
	return m.Register.validate()
}

func (p RegisterPolicy) validate() error {
	for surface, register := range p.Surfaces {
		if surface != SurfaceForge && surface != SurfaceDocs && surface != SurfaceAgent {
			return fmt.Errorf("unknown register surface %q", surface)
		}
		if !knownTextRegister(register) {
			return fmt.Errorf("unsupported text register %q", register)
		}
	}
	if len(p.Tasks) > MaxRegisterTaskRows {
		return fmt.Errorf("register tasks exceed %d rows", MaxRegisterTaskRows)
	}
	for _, label := range p.sortedTaskLabels() {
		if err := p.Tasks[label].validate(label); err != nil {
			return err
		}
	}
	return p.Evidence.validate()
}

func (t RegisterTask) validate(label string) error {
	if !router.ValidTaskLabel(label) {
		return fmt.Errorf("invalid register task label %q", label)
	}
	if !knownTextRegister(t.Register) {
		return fmt.Errorf("register task %q: unsupported text register %q", label, t.Register)
	}
	budgetWritten := t.maxTokensSet || t.MaxTokens != 0
	if budgetWritten && (t.MaxTokens < RegisterMaxTokensFloor || t.MaxTokens > RegisterMaxTokensCeiling) {
		return fmt.Errorf("register max_tokens for %q must be %d..%d", label, RegisterMaxTokensFloor, RegisterMaxTokensCeiling)
	}
	return nil
}

func (e EvidenceBounds) validate() error {
	if (e.linesSet || e.InlineMaxLines != 0) && (e.InlineMaxLines < 1 || e.InlineMaxLines > EvidenceInlineMaxLinesDefault) {
		return fmt.Errorf("register evidence bound must be 1..%d", EvidenceInlineMaxLinesDefault)
	}
	if (e.tokensSet || e.InlineMaxTokens != 0) && (e.InlineMaxTokens < 1 || e.InlineMaxTokens > EvidenceInlineMaxTokensDefault) {
		return fmt.Errorf("register evidence bound must be 1..%d", EvidenceInlineMaxTokensDefault)
	}
	return nil
}
