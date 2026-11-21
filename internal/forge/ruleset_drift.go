// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/util"
)

// RulesetDrift describes semantic differences from the rendered branch policy. The
// sorted context lists remain available for cause-based audit classification.
// Unwrap preserves errors.Is(err, ErrRulesetDrift).
type RulesetDrift struct {
	MissingContexts []string
	ExtraContexts   []string
	DifferingKeys   []string
	sources         map[string][]string
}

const (
	maxDriftItems      = 64
	maxDriftLabelBytes = 512
)

// Unwrap returns ErrRulesetDrift so errors.Is keeps matching the sentinel.
func (d *RulesetDrift) Unwrap() error { return ErrRulesetDrift }

// Error names each missing or extra required status context with its workflow and
// each differing key, bounded by maxDriftItems and maxRulesetContexts.
func (d *RulesetDrift) Error() string {
	parts := []string{ErrRulesetDrift.Error()}
	parts = append(parts, d.contextDescriptions("missing", d.MissingContexts)...)
	parts = append(parts, d.contextDescriptions("extra", d.ExtraContexts)...)
	for i := 0; i < len(d.DifferingKeys) && i < maxDriftItems; i++ {
		parts = append(parts, "differing key "+util.TruncateExcerpt(d.DifferingKeys[i], maxDriftLabelBytes))
	}
	if len(d.DifferingKeys) > maxDriftItems {
		parts = append(parts, fmt.Sprintf("%d more differing keys", len(d.DifferingKeys)-maxDriftItems))
	}
	return strings.Join(parts, "; ")
}

// contextDescriptions renders one bounded line per context of the given kind with the
// workflow files that report it, or "workflow unknown".
func (d *RulesetDrift) contextDescriptions(kind string, contexts []string) []string {
	var parts []string
	for i := 0; i < len(contexts) && i < maxRulesetContexts; i++ {
		source := "workflow unknown"
		if paths := d.sources[contexts[i]]; len(paths) > 0 {
			source = "workflow " + util.TruncateExcerpt(strings.Join(paths, ", "), maxDriftLabelBytes)
		}
		parts = append(parts, fmt.Sprintf("%s required status context %q (%s)", kind,
			util.TruncateExcerpt(contexts[i], maxDriftLabelBytes), source))
	}
	if len(contexts) > maxRulesetContexts {
		parts = append(parts, fmt.Sprintf("%d more %s required status contexts", len(contexts)-maxRulesetContexts, kind))
	}
	return parts
}

// ValidateRepositoryRulesetIn adds workflow provenance to ValidateRepositoryRuleset's
// drift error. It reads workflows only on drift, through the existing bounded reader
// and context selector. Declared external or removed contexts retain "workflow unknown".
func ValidateRepositoryRulesetIn(ctx context.Context, root string, data []byte, branch string, policy config.BranchProtectionPolicy, contexts []string) error {
	err := ValidateRepositoryRuleset(data, branch, policy, contexts)
	var drift *RulesetDrift
	if !errors.As(err, &drift) {
		return err
	}
	sources, sourceErr := rulesetWorkflowSources(ctx, root)
	drift.sources = sources
	if sourceErr != nil {
		return fmt.Errorf("%w; workflow source discovery failed: %w", err, sourceErr)
	}
	return err
}

// rulesetWorkflowSources maps each required status context to the workflow files that
// report it, through the bounded workflow reader and context selector, under a timeout.
func rulesetWorkflowSources(ctx context.Context, root string) (map[string][]string, error) {
	if ctx == nil {
		return nil, errors.New("workflow source discovery requires a context")
	}
	ctx, cancel := context.WithTimeout(ctx, workflowDiscoveryTimeout)
	defer cancel()
	files, err := readWorkflowFiles(ctx, root)
	if err != nil {
		return nil, err
	}
	identity, err := guardIdentity(files, root)
	if err != nil {
		return nil, err
	}
	called := newCalledWorkflows(files)
	sources := make(map[string][]string)
	for i := 0; i < len(files) && i < maxWorkflowFiles; i++ {
		contexts, err := filesContextsIn(files[i:i+1], called, identity)
		if err != nil {
			return nil, err
		}
		for j := 0; j < len(contexts) && j < maxRulesetContexts; j++ {
			path := plannedWorkflowDir + files[i].Name
			if !slices.Contains(sources[contexts[j]], path) {
				sources[contexts[j]] = append(sources[contexts[j]], path)
			}
		}
	}
	return sources, nil
}

// describeRulesetDrift collects the missing and extra required status contexts and the
// differing top-level and rule keys between the observed and expected rulesets.
func describeRulesetDrift(observed, expected map[string]any, data, expectedData []byte) *RulesetDrift {
	drift := &RulesetDrift{DifferingKeys: differingObjectKeys(observed, expected, "", "rules")}
	actual, actualErr := RulesetStatusContexts(data)
	wanted, wantedErr := RulesetStatusContexts(expectedData)
	if actualErr == nil && wantedErr == nil {
		drift.MissingContexts = missingDriftContexts(wanted, actual)
		drift.ExtraContexts = missingDriftContexts(actual, wanted)
	}
	drift.DifferingKeys = append(drift.DifferingKeys, differingRules(observed["rules"], expected["rules"])...)
	if len(drift.MissingContexts)+len(drift.ExtraContexts)+len(drift.DifferingKeys) == 0 {
		drift.DifferingKeys = append(drift.DifferingKeys, "rules (structure or order)")
	}
	slices.Sort(drift.DifferingKeys)
	drift.DifferingKeys = slices.Compact(drift.DifferingKeys)
	return drift
}

// missingDriftContexts returns the sorted, unique contexts in wanted that actual lacks.
func missingDriftContexts(wanted, actual []string) []string {
	present := make(map[string]bool, len(actual))
	for i := 0; i < len(actual) && i < maxRulesetContexts; i++ {
		present[actual[i]] = true
	}
	var missing []string
	for i := 0; i < len(wanted) && i < maxRulesetContexts; i++ {
		if !present[wanted[i]] {
			missing = append(missing, wanted[i])
		}
	}
	slices.Sort(missing)
	return slices.Compact(missing)
}

// differingObjectKeys compares one level only: conditions are named by their top-level
// key, rule parameters by their parameter key. No recursive document walker or parser.
func differingObjectKeys(observed, expected map[string]any, prefix, skip string) []string {
	keys := maps.Clone(expected)
	if keys == nil {
		keys = make(map[string]any)
	}
	maps.Copy(keys, observed)
	ordered := slices.Sorted(maps.Keys(keys))
	var differing []string
	for i := 0; i < len(ordered) && i < maxRulesetContexts; i++ {
		key := ordered[i]
		if skip != "" && key == skip {
			continue
		}
		left, leftOK := observed[key]
		right, rightOK := expected[key]
		if leftOK != rightOK || !reflect.DeepEqual(left, right) {
			differing = append(differing, prefix+key)
		}
	}
	if len(ordered) > maxRulesetContexts {
		differing = append(differing, prefix+"[key comparison limit exceeded]")
	}
	return differing
}

// differingRules names the rule types and rule keys that differ between the observed and
// expected rule lists, plus duplicate-type and order differences.
func differingRules(observed, expected any) []string {
	left, leftErr := objectList(observed, "rules", maxRulesetRules)
	right, rightErr := objectList(expected, "rules", maxRulesetRules)
	if leftErr != nil || rightErr != nil {
		return []string{"rules (invalid rule list)"}
	}
	leftIndex, rightIndex := firstRuleIndex(left), firstRuleIndex(right)
	names := maps.Clone(leftIndex)
	maps.Copy(names, rightIndex)
	ordered := slices.Sorted(maps.Keys(names))
	var keys []string
	for i := 0; i < len(ordered) && i < 2*maxRulesetRules; i++ {
		name := ordered[i]
		l, lok := leftIndex[name]
		r, rok := rightIndex[name]
		if !lok || !rok {
			keys = append(keys, absentRuleKeys(name, left, l, lok)...)
			continue
		}
		keys = append(keys, differingRuleKeys(left[l], right[r], name)...)
	}
	keys = append(keys, ruleStructureDifference(left, right, leftIndex, rightIndex)...)
	return keys
}

// absentRuleKeys names a rule present on one side only; a retained status-check rule
// only has its check list validated, since membership drift already explains it.
func absentRuleKeys(name string, observed []map[string]any, index int, present bool) []string {
	if name != statusChecksParameter {
		return []string{"rules." + name}
	}
	if !present {
		return nil
	}
	parameters, err := optionalObject(observed[index]["parameters"], "rules.required_status_checks.parameters")
	if err != nil {
		return []string{"rules.required_status_checks.parameters"}
	}
	checks := parameters[statusChecksParameter]
	// Comparing a retained status rule with itself validates its shape without
	// inventing parameter differences for a rule that membership drift explains.
	return differingStatusCheckKeys(checks, checks)
}

// ruleStructureDifference reports duplicate or missing rule types, or a different rule order.
func ruleStructureDifference(left, right []map[string]any, leftIndex, rightIndex map[string]int) []string {
	if len(leftIndex) != len(left) || len(rightIndex) != len(right) {
		return []string{"rules (duplicate or missing type)"}
	}
	if !sameRuleOrder(left, right) {
		return []string{"rules (order)"}
	}
	return nil
}

// sameRuleOrder compares the rule type order, ignoring the status-check rule unless both
// sides carry it.
func sameRuleOrder(left, right []map[string]any) bool {
	includeStatus := hasRuleType(left, statusChecksParameter) && hasRuleType(right, statusChecksParameter)
	var leftTypes, rightTypes []any
	for i := 0; i < len(left) && i < maxRulesetRules; i++ {
		if includeStatus || left[i]["type"] != statusChecksParameter {
			leftTypes = append(leftTypes, left[i]["type"])
		}
	}
	for i := 0; i < len(right) && i < maxRulesetRules; i++ {
		if includeStatus || right[i]["type"] != statusChecksParameter {
			rightTypes = append(rightTypes, right[i]["type"])
		}
	}
	return reflect.DeepEqual(leftTypes, rightTypes)
}

// differingRuleKeys names the differing keys and parameters of one rule type, including
// per-context status-check keys.
func differingRuleKeys(left, right map[string]any, name string) []string {
	prefix := "rules." + name + "."
	keys := differingObjectKeys(left, right, prefix, "parameters")
	leftRaw, leftPresent := left["parameters"]
	rightRaw, rightPresent := right["parameters"]
	if leftPresent != rightPresent || (leftRaw == nil) != (rightRaw == nil) {
		return append(keys, prefix+"parameters")
	}
	leftParams, leftErr := optionalObject(left["parameters"], prefix+"parameters")
	rightParams, rightErr := optionalObject(right["parameters"], prefix+"parameters")
	if leftErr != nil || rightErr != nil {
		return append(keys, prefix+"parameters")
	}
	skip := ""
	if name == statusChecksParameter {
		skip = statusChecksParameter
	}
	keys = append(keys, differingObjectKeys(leftParams, rightParams, prefix+"parameters.", skip)...)
	if name == statusChecksParameter {
		keys = append(keys, differingStatusCheckKeys(leftParams[statusChecksParameter], rightParams[statusChecksParameter])...)
	}
	return keys
}

// differingStatusCheckKeys names differing keys of status checks present on both sides,
// or the list itself when it is invalid, has duplicates, or differs only in order.
func differingStatusCheckKeys(observed, expected any) []string {
	const prefix = "rules.required_status_checks.parameters.required_status_checks"
	left, leftErr := objectList(observed, prefix, maxRulesetContexts)
	right, rightErr := objectList(expected, prefix, maxRulesetContexts)
	if leftErr != nil || rightErr != nil {
		return []string{prefix + " (invalid check list)"}
	}
	leftIndex, leftValid := statusCheckIndex(left)
	rightIndex, rightValid := statusCheckIndex(right)
	if !leftValid || !rightValid {
		return []string{prefix + " (invalid or duplicate context)"}
	}
	var keys []string
	names := slices.Sorted(maps.Keys(rightIndex))
	for i := 0; i < len(names) && i < maxRulesetContexts; i++ {
		name := names[i]
		if previous, ok := leftIndex[name]; ok {
			keys = append(keys, differingObjectKeys(previous, rightIndex[name], fmt.Sprintf("%s[%q].", prefix, name), "")...)
		}
	}
	if len(keys) == 0 && maps.EqualFunc(leftIndex, rightIndex, func(_, _ map[string]any) bool { return true }) && !reflect.DeepEqual(observed, expected) {
		keys = append(keys, prefix+" (order or structure)")
	}
	return keys
}

// statusCheckIndex indexes checks by context; false when a context is missing, empty or repeated.
func statusCheckIndex(checks []map[string]any) (map[string]map[string]any, bool) {
	index := make(map[string]map[string]any, len(checks))
	for i := 0; i < len(checks) && i < maxRulesetContexts; i++ {
		name, ok := checks[i]["context"].(string)
		if !ok || name == "" || index[name] != nil {
			return nil, false
		}
		index[name] = checks[i]
	}
	return index, true
}
