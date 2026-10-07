// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// Backlog caps bound how large each backlog category may grow (#792). They are policy keys
// like the complexity limits: a profile or facet, an external fleet, organization, deployment
// or workstation document and the repository's .standards.yaml each declare them under
// backlog.caps.<category>, one decoder (decodeBacklog) reads all of them, and the effective
// policy joins them. A cap only tightens: the lowest max and the strictest action win,
// whichever layer declares them, and EffectivePolicy.BacklogFields names the layers that set
// each value. A category no layer caps has no bound.

// The backlog categories a cap may name.
const (
	// BacklogDefects counts open defect rows of the bug ledger (.workingdir/BUGS.md).
	BacklogDefects = "defects"
	// BacklogTasks counts the pending task rows of the task ledger (.workingdir/OPEN.md).
	BacklogTasks = "tasks"
	// BacklogQuestions counts the pending questions of .workingdir/QUESTIONS.md.
	BacklogQuestions = "questions"
	// BacklogForgeAlerts names the forge's code-scanning, dependency and secret-scanning alerts.
	BacklogForgeAlerts = "forge_alerts"
)

// BacklogAction is what praetor does once a category is over its cap. Each action does what
// the weaker ones do: batch also reports, gate also reports and batches.
type BacklogAction string

// The backlog actions, weakest first.
const (
	// BacklogReport makes praetorctl state status mark the category over its cap.
	BacklogReport BacklogAction = "report"
	// BacklogBatch makes praetorctl state batch write the category's batch file.
	BacklogBatch BacklogAction = "batch"
	// BacklogGate makes praetorctl audit fail, naming the category, its count and its cap.
	BacklogGate BacklogAction = "gate"
)

// maxBacklogCap bounds a declared max; no ledger reader holds more rows (maxLedgerEntries in
// internal/state is 10000).
const maxBacklogCap = 1000000

// rank orders the actions by strictness: 0 for an undeclared action, -1 for an unknown one.
func (a BacklogAction) rank() int {
	switch a {
	case "":
		return 0
	case BacklogReport:
		return 1
	case BacklogBatch:
		return 2
	case BacklogGate:
		return 3
	}
	return -1
}

// Includes reports whether a category whose action is a does what other asks for: gate
// includes batch and report, batch includes report.
func (a BacklogAction) Includes(other BacklogAction) bool {
	return a.rank() >= other.rank()
}

// BacklogCap bounds one category. Max is the largest count within the cap, so the cap is
// inclusive: a count equal to Max is at the cap and passes, and only a count above Max is over
// it and triggers the action. A Max of zero declares no bound.
type BacklogCap struct {
	Max    int           `yaml:"max,omitempty" json:"max,omitzero"`
	Action BacklogAction `yaml:"action,omitempty" json:"action,omitzero"`
}

// Declared reports whether the cap bounds its category.
func (c BacklogCap) Declared() bool { return c.Max > 0 }

// EffectiveAction is the declared action, or report when no layer declared one.
func (c BacklogCap) EffectiveAction() BacklogAction {
	if c.Action == "" {
		return BacklogReport
	}
	return c.Action
}

// Over reports whether count is above the cap. An undeclared cap is never exceeded.
func (c BacklogCap) Over(count int) bool { return c.Declared() && count > c.Max }

// Limit is the declared max, zero when the cap is undeclared.
func (c BacklogCap) Limit() int { return c.Max }

// BacklogCaps holds one cap per category.
type BacklogCaps struct {
	Defects     BacklogCap `yaml:"defects,omitempty" json:"defects,omitzero"`
	Tasks       BacklogCap `yaml:"tasks,omitempty" json:"tasks,omitzero"`
	Questions   BacklogCap `yaml:"questions,omitempty" json:"questions,omitzero"`
	ForgeAlerts BacklogCap `yaml:"forge_alerts,omitempty" json:"forge_alerts,omitzero"`
}

// BacklogCategories lists the category keys in the order BacklogCaps.Caps returns them.
func BacklogCategories() [4]string {
	return [4]string{BacklogDefects, BacklogTasks, BacklogQuestions, BacklogForgeAlerts}
}

// Caps returns the cap of every category, in BacklogCategories order.
func (c BacklogCaps) Caps() [4]BacklogCap {
	return [4]BacklogCap{c.Defects, c.Tasks, c.Questions, c.ForgeAlerts}
}

func (c *BacklogCaps) pointers() [4]*BacklogCap {
	return [4]*BacklogCap{&c.Defects, &c.Tasks, &c.Questions, &c.ForgeAlerts}
}

// BacklogSection is the backlog section of a policy document. Only caps is defined.
type BacklogSection struct {
	Caps BacklogCaps `yaml:"caps,omitempty"`
}

// UnmarshalYAML decodes through decodeBacklog, so a misspelled category, key or action fails
// the document and names the key: yaml.Node.Decode drops the caller's KnownFields setting.
func (s *BacklogSection) UnmarshalYAML(node *yaml.Node) error {
	caps, err := decodeBacklog(node)
	if err != nil {
		return err
	}
	s.Caps = caps
	return nil
}

// decodeBacklog is the one reader of a backlog section, for every layer kind.
func decodeBacklog(node *yaml.Node) (BacklogCaps, error) {
	var caps BacklogCaps
	if node == nil {
		return caps, nil
	}
	if node.Kind != yaml.MappingNode {
		return caps, errors.New("backlog must be a mapping")
	}
	if err := requireKnownKeys(node, "backlog", []string{"caps"}); err != nil {
		return caps, err
	}
	section := policyMember(node, "caps")
	if section == nil {
		return caps, nil
	}
	if section.Kind != yaml.MappingNode {
		return caps, errors.New("backlog.caps must be a mapping")
	}
	names, targets := BacklogCategories(), caps.pointers()
	for i := 0; i+1 < len(section.Content) && i < maxPolicyNodes; i += 2 {
		key := section.Content[i].Value
		index := slices.Index(names[:], key)
		if index < 0 {
			return BacklogCaps{}, fmt.Errorf("backlog.caps: unknown category %q; known categories are %s",
				key, strings.Join(names[:], ", "))
		}
		decoded, err := decodeBacklogCap("backlog.caps."+key, section.Content[i+1])
		if err != nil {
			return BacklogCaps{}, err
		}
		*targets[index] = decoded
	}
	return caps, nil
}

// decodeBacklogCap reads one category's max and action. A category must declare at least one.
func decodeBacklogCap(path string, node *yaml.Node) (BacklogCap, error) {
	var result BacklogCap
	if node.Kind != yaml.MappingNode {
		return result, fmt.Errorf("%s must be a mapping", path)
	}
	if err := requireKnownKeys(node, path, []string{"max", "action"}); err != nil {
		return result, err
	}
	limit, action := policyMember(node, "max"), policyMember(node, "action")
	if limit == nil && action == nil {
		return result, fmt.Errorf("%s declares neither max nor action", path)
	}
	if limit != nil {
		if limit.Kind != yaml.ScalarNode || limit.Tag != "!!int" || limit.Decode(&result.Max) != nil ||
			result.Max <= 0 || result.Max > maxBacklogCap {
			return BacklogCap{}, fmt.Errorf("%s.max must be an integer from 1 to %d", path, maxBacklogCap)
		}
	}
	if action != nil {
		result.Action = BacklogAction(action.Value)
		if action.Kind != yaml.ScalarNode || action.Tag != "!!str" || result.Action.rank() <= 0 {
			return BacklogCap{}, fmt.Errorf("%s.action %q must be report, batch or gate", path, action.Value)
		}
	}
	return result, nil
}

// validate holds a layer's caps, decoded or built by a ResolvePolicy caller, to the decoder's
// bounds: a max from 0 (undeclared) to maxBacklogCap and a known or undeclared action.
func (c BacklogCaps) validate() error {
	names := BacklogCategories()
	for i, entry := range c.Caps() {
		if entry.Max < 0 || entry.Max > maxBacklogCap {
			return fmt.Errorf("backlog.caps.%s.max must be an integer from 1 to %d", names[i], maxBacklogCap)
		}
		if entry.Action.rank() < 0 {
			return fmt.Errorf("backlog.caps.%s.action %q must be report, batch or gate", names[i], entry.Action)
		}
	}
	return nil
}

// validateResolved refuses an action no layer gave a max to act on: the action would bound
// nothing while reading as configured.
func (c BacklogCaps) validateResolved(fields map[string][]string) error {
	names := BacklogCategories()
	for i, entry := range c.Caps() {
		if entry.Action != "" && entry.Max == 0 {
			return fmt.Errorf("backlog.caps.%s.action is set by %s, but no layer sets backlog.caps.%s.max",
				names[i], strings.Join(fields[backlogField(names[i], "action")], ", "), names[i])
		}
	}
	return nil
}

// joinBacklog keeps the lower declared max and the stricter action of every category. An
// unknown action is preserved so validation can reject it.
func joinBacklog(a, b BacklogCaps) BacklogCaps {
	result := a
	targets, others := result.pointers(), b.Caps()
	for i, target := range targets {
		target.Max = minPositive(target.Max, others[i].Max)
		target.Action = stricterAction(target.Action, others[i].Action)
	}
	return result
}

// stricterAction keeps the stricter of two actions; an unknown one wins so validation sees it.
func stricterAction(a, b BacklogAction) BacklogAction {
	switch {
	case a.rank() < 0:
		return a
	case b.rank() < 0 || b.rank() > a.rank():
		return b
	}
	return a
}

// backlogField is the provenance key of one category value: backlog.caps.<category>.<key>.
func backlogField(category, key string) string {
	return "backlog.caps." + category + "." + key
}

// applyBacklog records which layer imposes each cap value before the layer is joined: a
// tighter value makes the layer the only contributor, an equal one adds it beside the others.
func (p *EffectivePolicy) applyBacklog(source string, caps BacklogCaps) error {
	if err := caps.validate(); err != nil {
		return fmt.Errorf("policy source %q: %w", source, err)
	}
	names, current, declared := BacklogCategories(), p.Policy.Backlog.Caps(), caps.Caps()
	for i := range names {
		if declared[i].Max > 0 {
			p.recordBacklog(backlogField(names[i], "max"), source, current[i].Max == 0 || declared[i].Max < current[i].Max,
				declared[i].Max == current[i].Max)
		}
		if declared[i].Action != "" {
			p.recordBacklog(backlogField(names[i], "action"), source, declared[i].Action.rank() > current[i].Action.rank(),
				declared[i].Action == current[i].Action)
		}
	}
	return nil
}

func (p *EffectivePolicy) recordBacklog(field, source string, tighter, equal bool) {
	if p.BacklogFields == nil {
		p.BacklogFields = map[string][]string{}
	}
	switch {
	case tighter:
		p.BacklogFields[field] = []string{source}
	case equal:
		p.BacklogFields[field] = append(p.BacklogFields[field], source)
	}
}

// BacklogSources returns the layers that set a category's max and action, in layer order.
func (p *EffectivePolicy) BacklogSources(category string) (maxBy, actionBy []string) {
	if p == nil {
		return nil, nil
	}
	return slices.Clone(p.BacklogFields[backlogField(category, "max")]),
		slices.Clone(p.BacklogFields[backlogField(category, "action")])
}

// verifyBacklogFields checks a retained snapshot: every declared cap value names its layers,
// every named layer is a retained source, and no field names a value the policy lacks.
func verifyBacklogFields(caps BacklogCaps, fields map[string][]string, seen map[string]bool) error {
	want := map[string]bool{}
	names := BacklogCategories()
	for i, entry := range caps.Caps() {
		want[backlogField(names[i], "max")] = entry.Max > 0
		want[backlogField(names[i], "action")] = entry.Action != ""
	}
	for key, contributors := range fields {
		if !want[key] || len(contributors) == 0 || len(contributors) > len(seen) {
			return errors.New("effective backlog cap contributors do not match the resolved caps")
		}
		for i := 0; i < len(contributors) && i <= maxPolicyLayers; i++ {
			if !seen[contributors[i]] {
				return errors.New("effective backlog cap contributor has no source")
			}
		}
	}
	for key, declared := range want {
		if declared && len(fields[key]) == 0 {
			return errors.New("effective backlog cap has no contributor")
		}
	}
	return caps.validateResolved(fields)
}

// backlogEvidence is the evidence line of every declared cap, empty when none is declared.
func (p *EffectivePolicy) backlogEvidence() string {
	var evidence strings.Builder
	names := BacklogCategories()
	for i, entry := range p.Policy.Backlog.Caps() {
		if !entry.Declared() {
			continue
		}
		maxBy, actionBy := p.BacklogSources(names[i])
		fmt.Fprintf(&evidence, "\n  backlog.caps.%s: max=%d (%s) action=%s (%s)", names[i], entry.Limit(),
			evidenceContributors(maxBy), entry.EffectiveAction(), BacklogOrigin(actionBy))
	}
	return evidence.String()
}

// BacklogOrigin names the layers that set a cap value, or says the value is the default (an
// action no layer declared is report).
func BacklogOrigin(layers []string) string {
	if len(layers) == 0 {
		return "default"
	}
	return evidenceContributors(layers)
}
