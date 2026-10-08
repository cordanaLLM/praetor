package forge

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"strings"
)

// maxRulesetRefs bounds the ref patterns of one ruleset condition (HISS-02).
const maxRulesetRefs = 256

// statusChecksParameter is the required_status_checks rule parameter listing contexts.
const statusChecksParameter = "required_status_checks"

// jsonObject decodes data as one JSON object, keeping numbers as json.Number so a value
// read from the forge is written back and compared exactly as the forge sent it.
func jsonObject(data []byte) (map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var object map[string]any
	if err := decoder.Decode(&object); err != nil {
		return nil, err
	}
	if object == nil {
		return nil, errors.New("expected a JSON object, got null")
	}
	return object, nil
}

// normalizeRuleset turns a rendered ruleset into the generic JSON shape jsonObject
// yields, so rendered and forge-read rulesets merge and compare value for value.
func normalizeRuleset(doc map[string]any) (map[string]any, error) {
	data, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("encode ruleset: %w", err)
	}
	return jsonObject(data)
}

// mergeRuleset overlays the desired praetor ruleset onto the live one without narrowing
// it. Name, target, enforcement and every parameter praetor renders come from desired, a
// relaxation the policy declares included, and each rendered parameter whose live value was
// stricter is returned as lowered (loweredParameters) for sync --remote to report. The live
// ruleset keeps its bypass actors, other conditions, every ref it includes, every status check
// context it requires, and every rule and rule parameter praetor does not render. Removing
// any of those from a live ruleset is a deliberate manual change.
func mergeRuleset(live, desired map[string]any) (map[string]any, []LoweredParameter, error) {
	merged := map[string]any{"name": desired["name"], "target": desired["target"], "enforcement": desired["enforcement"]}
	if actors, present := live["bypass_actors"]; present {
		merged["bypass_actors"] = actors
	}
	conditions, err := mergeConditions(live["conditions"], desired["conditions"])
	if err != nil {
		return nil, nil, err
	}
	merged["conditions"] = conditions
	rules, lowered, err := mergeRules(live["rules"], desired["rules"])
	if err != nil {
		return nil, nil, err
	}
	merged["rules"] = rules
	return merged, lowered, nil
}

// mergeConditions unions the ref_name includes and drops desired refs from the excludes,
// keeping any other live condition as it is.
func mergeConditions(liveRaw, desiredRaw any) (map[string]any, error) {
	live, err := optionalObject(liveRaw, "conditions")
	if err != nil {
		return nil, err
	}
	desired, err := optionalObject(desiredRaw, "conditions")
	if err != nil {
		return nil, err
	}
	liveRef, err := optionalObject(live["ref_name"], "conditions.ref_name")
	if err != nil {
		return nil, err
	}
	desiredRef, err := optionalObject(desired["ref_name"], "conditions.ref_name")
	if err != nil {
		return nil, err
	}
	liveInclude, liveExclude, err := refPatterns(liveRef)
	if err != nil {
		return nil, err
	}
	desiredInclude, _, err := refPatterns(desiredRef)
	if err != nil {
		return nil, err
	}
	merged := make(map[string]any, len(live)+1)
	maps.Copy(merged, live)
	include := appendMissing(liveInclude, desiredInclude)
	if len(include) > maxRulesetRefs {
		return nil, fmt.Errorf("merged ruleset includes more than %d refs", maxRulesetRefs)
	}
	merged["ref_name"] = map[string]any{"include": toAnyList(include), "exclude": toAnyList(removeAll(liveExclude, desiredInclude))}
	return merged, nil
}

// mergeRules merges each desired rule into the live rule of the same type and keeps every
// other live rule, in the live order after the desired ones. It also returns the rendered
// parameters the merge lowered.
func mergeRules(liveRaw, desiredRaw any) ([]any, []LoweredParameter, error) {
	live, err := objectList(liveRaw, "rules", maxRulesetRules)
	if err != nil {
		return nil, nil, err
	}
	desired, err := objectList(desiredRaw, "rules", maxRulesetRules)
	if err != nil {
		return nil, nil, err
	}
	merged, err := mergeDesiredRules(live, desired)
	if err != nil {
		return nil, nil, err
	}
	for i := 0; i < len(live) && i < maxRulesetRules; i++ {
		if !merged.consumed[i] {
			merged.rules = append(merged.rules, live[i])
		}
	}
	if hasRuleType(desired, codeScanningRule) {
		dropDefaultSetupChecks(merged.rules)
	}
	if len(merged.rules) > maxRulesetRules {
		return nil, nil, fmt.Errorf("merged ruleset exceeds %d rules", maxRulesetRules)
	}
	return merged.rules, merged.lowered, nil
}

// desiredRulesMerge is what mergeDesiredRules returns: the desired rules, each merged into the
// first live rule of its type, the indexes of the live rules consumed that way, and the
// rendered parameters those merges lowered.
type desiredRulesMerge struct {
	rules    []any
	consumed map[int]bool
	lowered  []LoweredParameter
}

// mergeDesiredRules merges each desired rule into the first live rule of its type.
func mergeDesiredRules(live, desired []map[string]any) (desiredRulesMerge, error) {
	firstLive := firstRuleIndex(live)
	merged := desiredRulesMerge{rules: make([]any, 0, len(live)+len(desired)), consumed: make(map[int]bool, len(desired))}
	for i := 0; i < len(desired) && i < maxRulesetRules; i++ {
		ruleType, isString := desired[i]["type"].(string)
		if !isString {
			return desiredRulesMerge{}, fmt.Errorf("desired rule %d has no type", i)
		}
		index, found := firstLive[ruleType]
		if !found {
			merged.rules = append(merged.rules, desired[i])
			continue
		}
		rule, lowered, err := mergeRule(live[index], desired[i], ruleType)
		if err != nil {
			return desiredRulesMerge{}, err
		}
		merged.consumed[index] = true
		merged.rules = append(merged.rules, rule)
		merged.lowered = append(merged.lowered, lowered...)
	}
	return merged, nil
}

// mergeRule keeps the live rule's own keys and parameters and overlays the desired ones. It
// returns the desired parameters that lowered a stricter live value (loweredParameters).
func mergeRule(live, desired map[string]any, ruleType string) (map[string]any, []LoweredParameter, error) {
	liveParams, err := optionalObject(live["parameters"], ruleType+".parameters")
	if err != nil {
		return nil, nil, err
	}
	desiredParams, err := optionalObject(desired["parameters"], ruleType+".parameters")
	if err != nil {
		return nil, nil, err
	}
	rule := make(map[string]any, len(live))
	maps.Copy(rule, live)
	if len(liveParams) == 0 && len(desiredParams) == 0 {
		return rule, nil, nil
	}
	params, err := mergeRuleParameters(ruleType, liveParams, desiredParams)
	if err != nil {
		return nil, nil, err
	}
	rule["parameters"] = params
	return rule, loweredParameters(ruleType, liveParams, desiredParams), nil
}

// mergeRuleParameters merges the desired parameters of a rule over the live ones. Most rules take
// the declared value for every parameter praetor renders. The two rules a merge queue renders
// take no declared values (there is no policy key for the queue sizes, timeout or grouping, nor
// for the scanning thresholds), so the live ones are the operator's: a live merge_queue keeps
// every parameter it has, and a live code_scanning keeps every tool it lists (#893).
func mergeRuleParameters(ruleType string, liveParams, desiredParams map[string]any) (map[string]any, error) {
	params := make(map[string]any, len(liveParams)+len(desiredParams))
	switch ruleType {
	case MergeQueueRule:
		maps.Copy(params, desiredParams)
		maps.Copy(params, liveParams)
		return params, nil
	case codeScanningRule:
		maps.Copy(params, liveParams)
		maps.Copy(params, desiredParams)
		tools, err := unionCodeScanningTools(liveParams[codeScanningToolsParameter], desiredParams[codeScanningToolsParameter])
		if err != nil {
			return nil, err
		}
		params[codeScanningToolsParameter] = tools
		return params, nil
	}
	maps.Copy(params, liveParams)
	maps.Copy(params, desiredParams)
	if ruleType == statusChecksParameter {
		checks, err := unionStatusChecks(liveParams[statusChecksParameter], desiredParams[statusChecksParameter])
		if err != nil {
			return nil, err
		}
		params[statusChecksParameter] = checks
	}
	return params, nil
}

// codeScanningToolsParameter is the code_scanning rule parameter that lists the required tools.
const codeScanningToolsParameter = "code_scanning_tools"

// maxCodeScanningTools bounds the tools one code_scanning rule lists (HISS-02).
const maxCodeScanningTools = 64

// unionCodeScanningTools keeps every live tool entry as it is, thresholds included, and appends
// each desired tool the live rule does not list yet.
func unionCodeScanningTools(liveRaw, desiredRaw any) ([]any, error) {
	live, err := objectList(liveRaw, codeScanningToolsParameter, maxCodeScanningTools)
	if err != nil {
		return nil, err
	}
	desired, err := objectList(desiredRaw, codeScanningToolsParameter, maxCodeScanningTools)
	if err != nil {
		return nil, err
	}
	merged := make([]any, 0, len(live)+len(desired))
	present := make(map[any]bool, len(live))
	for i := 0; i < len(live) && i < maxCodeScanningTools; i++ {
		present[live[i]["tool"]] = true
		merged = append(merged, live[i])
	}
	for i := 0; i < len(desired) && i < maxCodeScanningTools; i++ {
		if !present[desired[i]["tool"]] {
			merged = append(merged, desired[i])
		}
	}
	if len(merged) > maxCodeScanningTools {
		return nil, fmt.Errorf("merged %s exceed %d tools", codeScanningToolsParameter, maxCodeScanningTools)
	}
	return merged, nil
}

// LoweredParameter is a rendered ruleset parameter whose live value was stricter than the
// declared one and that a merge set to the declared value. A relaxation the policy declares,
// such as review_mode single_maintainer's zero approving reviews, reconciles like any other
// value; sync --remote names each lowered parameter, so no hosted setting drops silently (#154).
type LoweredParameter struct {
	// Rule is the ruleset rule type the parameter belongs to, such as pull_request.
	Rule string
	// Parameter is the rule parameter, such as required_approving_review_count.
	Parameter string
	// Live is the value the live ruleset carried before the write.
	Live any
	// Declared is the value written, which the resolved policy declares.
	Declared any
}

// stricterParameters are the rendered rule parameters that only tighten a rule as they grow:
// a count that is stricter when higher, and requirements that are stricter when true. Each
// belongs to the pull_request or required_status_checks rule, and their names do not recur in
// any other rule type.
var stricterParameters = []string{
	"required_approving_review_count",
	"dismiss_stale_reviews_on_push",
	"require_code_owner_review",
	"require_last_push_approval",
	"required_review_thread_resolution",
	"strict_required_status_checks_policy",
}

// loweredParameters returns each parameter of stricterParameters whose live value is stricter
// than the desired one that replaces it, in stricterParameters order. A value of another JSON
// type than its counterpart cannot be ordered and is not reported.
func loweredParameters(ruleType string, live, desired map[string]any) []LoweredParameter {
	var lowered []LoweredParameter
	for i := 0; i < len(stricterParameters); i++ {
		key := stricterParameters[i]
		liveValue, inLive := live[key]
		desiredValue, inDesired := desired[key]
		if inLive && inDesired && parameterStricter(liveValue, desiredValue) {
			lowered = append(lowered, LoweredParameter{Rule: ruleType, Parameter: key, Live: liveValue, Declared: desiredValue})
		}
	}
	return lowered
}

// parameterStricter reports whether the live value tightens a rule more than the desired one:
// true against false, or a larger integer. Anything else, mixed types included, is not.
func parameterStricter(live, desired any) bool {
	if liveFlag, isBool := live.(bool); isBool {
		desiredFlag, desiredBool := desired.(bool)
		return desiredBool && liveFlag && !desiredFlag
	}
	liveCount, liveErr := jsonInteger(live)
	desiredCount, desiredErr := jsonInteger(desired)
	return liveErr == nil && desiredErr == nil && liveCount > desiredCount
}

// jsonInteger reads an integer decoded by jsonObject (json.Number) or built in Go (int).
func jsonInteger(value any) (int64, error) {
	switch typed := value.(type) {
	case json.Number:
		return typed.Int64()
	case int:
		return int64(typed), nil
	default:
		return 0, fmt.Errorf("%v is not an integer", value)
	}
}

// unionStatusChecks keeps every live required check, integration binding included, and
// appends each desired context the live rule does not require yet.
func unionStatusChecks(liveRaw, desiredRaw any) ([]any, error) {
	live, err := objectList(liveRaw, "required_status_checks", maxRulesetContexts)
	if err != nil {
		return nil, err
	}
	desired, err := objectList(desiredRaw, "required_status_checks", maxRulesetContexts)
	if err != nil {
		return nil, err
	}
	merged := make([]any, 0, len(live)+len(desired))
	present := make(map[any]bool, len(live))
	for i := 0; i < len(live) && i < maxRulesetContexts; i++ {
		present[live[i]["context"]] = true
		merged = append(merged, live[i])
	}
	for i := 0; i < len(desired) && i < maxRulesetContexts; i++ {
		if !present[desired[i]["context"]] {
			merged = append(merged, desired[i])
		}
	}
	if len(merged) > maxRulesetContexts {
		return nil, fmt.Errorf("merged required status checks exceed %d contexts", maxRulesetContexts)
	}
	return merged, nil
}

// rulesetConverged reports how a ruleset read back from the forge falls short of want:
// a different name, target or enforcement, a wanted ref not included (or excluded), a
// missing rule, or a rule parameter with another value. Extra refs, rules, parameters and
// status checks the forge reports are not a shortfall.
func rulesetConverged(got, want map[string]any) error {
	for _, key := range []string{"name", "target", "enforcement"} {
		if !reflect.DeepEqual(got[key], want[key]) {
			return fmt.Errorf("ruleset readback %s is %v, want %v", key, got[key], want[key])
		}
	}
	if err := refsConverged(got, want); err != nil {
		return err
	}
	return rulesConverged(got["rules"], want["rules"])
}

func rulesConverged(gotRaw, wantRaw any) error {
	gotRules, err := objectList(gotRaw, "rules", maxRulesetRules)
	if err != nil {
		return fmt.Errorf("ruleset readback: %w", err)
	}
	wantRules, err := objectList(wantRaw, "rules", maxRulesetRules)
	if err != nil {
		return err
	}
	firstGot := firstRuleIndex(gotRules)
	for i := 0; i < len(wantRules) && i < maxRulesetRules; i++ {
		ruleType, isString := wantRules[i]["type"].(string)
		if !isString {
			return fmt.Errorf("wanted rule %d has no type", i)
		}
		index, found := firstGot[ruleType]
		if !found {
			return fmt.Errorf("ruleset readback lacks rule %q", ruleType)
		}
		if err := parametersConverged(ruleType, gotRules[index]["parameters"], wantRules[i]["parameters"]); err != nil {
			return err
		}
	}
	return nil
}

func refsConverged(got, want map[string]any) error {
	gotConditions, err := optionalObject(got["conditions"], "conditions")
	if err != nil {
		return fmt.Errorf("ruleset readback: %w", err)
	}
	wantConditions, err := optionalObject(want["conditions"], "conditions")
	if err != nil {
		return err
	}
	gotRef, err := optionalObject(gotConditions["ref_name"], "conditions.ref_name")
	if err != nil {
		return fmt.Errorf("ruleset readback: %w", err)
	}
	wantRef, err := optionalObject(wantConditions["ref_name"], "conditions.ref_name")
	if err != nil {
		return err
	}
	gotInclude, gotExclude, err := refPatterns(gotRef)
	if err != nil {
		return fmt.Errorf("ruleset readback: %w", err)
	}
	wantInclude, _, err := refPatterns(wantRef)
	if err != nil {
		return err
	}
	if missing := removeAll(wantInclude, gotInclude); len(missing) > 0 {
		return fmt.Errorf("ruleset readback does not include refs %v", missing)
	}
	// wantInclude minus its refs that are not excluded leaves exactly the excluded ones.
	if excluded := removeAll(wantInclude, removeAll(wantInclude, gotExclude)); len(excluded) > 0 {
		return fmt.Errorf("ruleset readback excludes protected refs %v", excluded)
	}
	return nil
}

func parametersConverged(ruleType string, gotRaw, wantRaw any) error {
	got, err := optionalObject(gotRaw, ruleType+".parameters")
	if err != nil {
		return fmt.Errorf("ruleset readback: %w", err)
	}
	want, err := optionalObject(wantRaw, ruleType+".parameters")
	if err != nil {
		return err
	}
	for key, value := range want {
		if ruleType == statusChecksParameter && key == statusChecksParameter {
			if err := statusChecksConverged(got[key], value); err != nil {
				return err
			}
			continue
		}
		if !reflect.DeepEqual(got[key], value) {
			return fmt.Errorf("ruleset readback rule %q parameter %q is %v, want %v", ruleType, key, got[key], value)
		}
	}
	return nil
}

func statusChecksConverged(gotRaw, wantRaw any) error {
	got, err := objectList(gotRaw, "required_status_checks", maxRulesetContexts)
	if err != nil {
		return fmt.Errorf("ruleset readback: %w", err)
	}
	want, err := objectList(wantRaw, "required_status_checks", maxRulesetContexts)
	if err != nil {
		return err
	}
	present := make(map[any]bool, len(got))
	for i := 0; i < len(got) && i < maxRulesetContexts; i++ {
		present[got[i]["context"]] = true
	}
	for i := 0; i < len(want) && i < maxRulesetContexts; i++ {
		if !present[want[i]["context"]] {
			return fmt.Errorf("ruleset readback does not require status check %v", want[i]["context"])
		}
	}
	return nil
}

// optionalObject reads a JSON object that may be absent; anything else present is an error.
func optionalObject(raw any, field string) (map[string]any, error) {
	if raw == nil {
		return map[string]any{}, nil
	}
	object, isObject := raw.(map[string]any)
	if !isObject {
		return nil, fmt.Errorf("ruleset field %s is not an object", field)
	}
	return object, nil
}

// objectList reads a JSON array of objects that may be absent, bounded by limit.
func objectList(raw any, field string, limit int) ([]map[string]any, error) {
	if raw == nil {
		return nil, nil
	}
	items, isList := raw.([]any)
	if !isList {
		return nil, fmt.Errorf("ruleset field %s is not an array", field)
	}
	if len(items) > limit {
		return nil, fmt.Errorf("ruleset field %s exceeds %d entries", field, limit)
	}
	objects := make([]map[string]any, 0, len(items))
	for i := 0; i < len(items) && i < limit; i++ {
		object, isObject := items[i].(map[string]any)
		if !isObject {
			return nil, fmt.Errorf("ruleset field %s entry %d is not an object", field, i)
		}
		objects = append(objects, object)
	}
	return objects, nil
}

// refPatterns reads the include and exclude ref patterns of a ref_name condition.
func refPatterns(ref map[string]any) (include, exclude []string, err error) {
	if include, err = stringList(ref["include"], "conditions.ref_name.include"); err != nil {
		return nil, nil, err
	}
	if exclude, err = stringList(ref["exclude"], "conditions.ref_name.exclude"); err != nil {
		return nil, nil, err
	}
	return include, exclude, nil
}

func stringList(raw any, field string) ([]string, error) {
	if raw == nil {
		return nil, nil
	}
	items, isList := raw.([]any)
	if !isList {
		return nil, fmt.Errorf("ruleset field %s is not an array", field)
	}
	if len(items) > maxRulesetRefs {
		return nil, fmt.Errorf("ruleset field %s exceeds %d entries", field, maxRulesetRefs)
	}
	values := make([]string, 0, len(items))
	for i := 0; i < len(items) && i < maxRulesetRefs; i++ {
		value, isString := items[i].(string)
		if !isString {
			return nil, fmt.Errorf("ruleset field %s entry %d is not a string", field, i)
		}
		values = append(values, value)
	}
	return values, nil
}

// firstRuleIndex maps each rule type to the index of its first occurrence.
func firstRuleIndex(rules []map[string]any) map[string]int {
	index := make(map[string]int, len(rules))
	for i := 0; i < len(rules) && i < maxRulesetRules; i++ {
		ruleType, isString := rules[i]["type"].(string)
		if _, seen := index[ruleType]; isString && !seen {
			index[ruleType] = i
		}
	}
	return index
}

// appendMissing returns base followed by each value of extra that base lacks.
func appendMissing(base, extra []string) []string {
	out := append([]string(nil), base...)
	for i := 0; i < len(extra) && i < maxRulesetRefs; i++ {
		if !containsRef(out, extra[i]) {
			out = append(out, extra[i])
		}
	}
	return out
}

// removeAll returns the values of list that drop does not contain.
func removeAll(list, drop []string) []string {
	out := make([]string, 0, len(list))
	for i := 0; i < len(list) && i < maxRulesetRefs; i++ {
		if !containsRef(drop, list[i]) {
			out = append(out, list[i])
		}
	}
	return out
}

func containsRef(list []string, value string) bool {
	for i := 0; i < len(list) && i < maxRulesetRefs; i++ {
		if list[i] == value {
			return true
		}
	}
	return false
}

func toAnyList(values []string) []any {
	out := make([]any, 0, len(values))
	for i := 0; i < len(values) && i < maxRulesetRefs; i++ {
		out = append(out, values[i])
	}
	return out
}

// hasRuleType reports whether rules holds a rule of type ruleType.
func hasRuleType(rules []map[string]any, ruleType string) bool {
	for i := 0; i < len(rules) && i < maxRulesetRules; i++ {
		if rules[i]["type"] == ruleType {
			return true
		}
	}
	return false
}

// IsDefaultSetupContext reports whether a status context is one CodeQL default setup reports,
// "CodeQL" or "CodeQL / Analyze (<language>)" or "Analyze (<language>)": default setup never
// runs for a merge group, so a queue-protected branch must not require it as a status check (it
// requires the results through a code_scanning rule instead).
func IsDefaultSetupContext(context string) bool {
	return context == "CodeQL" || strings.HasPrefix(context, "CodeQL / ") || strings.HasPrefix(context, "Analyze (")
}

// dropDefaultSetupChecks removes, in place, every CodeQL default setup context (IsDefaultSetupContext)
// from the required_status_checks rules among rules. The union merge keeps every live required
// check, so a context an earlier sync wrote would otherwise stay required and stall the queue.
func dropDefaultSetupChecks(rules []any) {
	for i := 0; i < len(rules) && i < maxRulesetRules; i++ {
		rule, ok := rules[i].(map[string]any)
		if !ok || rule["type"] != statusChecksParameter {
			continue
		}
		params, ok := rule["parameters"].(map[string]any)
		if !ok {
			continue
		}
		if checks, isList := params[statusChecksParameter].([]any); isList {
			params[statusChecksParameter] = withoutDefaultSetupChecks(checks)
		}
	}
}

// withoutDefaultSetupChecks returns checks less the entries whose context is a CodeQL default
// setup context.
func withoutDefaultSetupChecks(checks []any) []any {
	kept := make([]any, 0, len(checks))
	for j := 0; j < len(checks) && j < maxRulesetContexts; j++ {
		check, isObject := checks[j].(map[string]any)
		name, isString := "", false
		if isObject {
			name, isString = check["context"].(string)
		}
		if !isString || !IsDefaultSetupContext(name) {
			kept = append(kept, checks[j])
		}
	}
	return kept
}
