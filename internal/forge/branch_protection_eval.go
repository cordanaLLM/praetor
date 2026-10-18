package forge

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/cordanaLLM/praetor/internal/config"
)

// ProtectionVerdict classifies one declared branch protection property against the branch.
type ProtectionVerdict string

const (
	// ProtectionEnforced means the branch enforces the property as declared.
	ProtectionEnforced ProtectionVerdict = "enforced"
	// ProtectionDrift means the property is declared and the branch enforces less or nothing.
	ProtectionDrift ProtectionVerdict = "drift"
	// ProtectionStricter means the branch enforces more than declared. sync --remote lowers a
	// parameter it renders into its own ruleset to the declared value and names it
	// (LoweredParameter); a rule it does not render, another ruleset and the legacy protection
	// object it leaves as they are, so lowering those is the operator's change.
	ProtectionStricter ProtectionVerdict = "stricter"
	// ProtectionNotRequired means the property is neither declared nor enforced.
	ProtectionNotRequired ProtectionVerdict = "not-required"
)

// ProtectionFinding is one property compared: what the resolved policy declares, what the
// branch enforces, and the mechanisms that enforce it (LiveBranchProtection.Mechanisms), none
// when nothing does.
type ProtectionFinding struct {
	Property   string
	Declared   string
	Live       string
	EnforcedBy []string
	Verdict    ProtectionVerdict
}

// liveFlag is an on-or-off requirement and the mechanisms that switch it on.
type liveFlag struct {
	on bool
	by []string
}

func (f *liveFlag) set(by string) {
	f.on = true
	f.by = appendMissing(f.by, []string{by})
}

// liveCount is the highest approving review count any mechanism requires, and the mechanisms
// that require that count.
type liveCount struct {
	n  int
	by []string
}

func (c *liveCount) raise(n int, by string) {
	switch {
	case n > c.n:
		c.n, c.by = n, []string{by}
	case n == c.n && n > 0:
		c.by = appendMissing(c.by, []string{by})
	}
}

// liveEnforcement is the union of what every mechanism enforces on a branch, which is what
// GitHub applies when rulesets and legacy protection both target it.
type liveEnforcement struct {
	pullRequest, codeOwner, dismissStale liveFlag
	signatures, linearHistory            liveFlag
	deletionBlocked, forcePushBlocked    liveFlag
	reviews                              liveCount
	checks                               map[string][]string
}

// pullRequestParameters are the pull_request rule parameters the declared policy sets.
type pullRequestParameters struct {
	RequiredApprovingReviewCount int  `json:"required_approving_review_count"`
	DismissStaleReviewsOnPush    bool `json:"dismiss_stale_reviews_on_push"`
	RequireCodeOwnerReview       bool `json:"require_code_owner_review"`
}

// statusCheckParameters are the required_status_checks rule parameters naming the checks.
type statusCheckParameters struct {
	RequiredStatusChecks []StatusCheckRef `json:"required_status_checks"`
}

// collectEnforcement joins every rule and the legacy protection object of live.
func collectEnforcement(live *LiveBranchProtection) (*liveEnforcement, error) {
	e := &liveEnforcement{checks: map[string][]string{}}
	if len(live.Rules) > maxBranchRules {
		return nil, fmt.Errorf("branch %s carries more than %d active rules", live.Branch, maxBranchRules)
	}
	for i := 0; i < len(live.Rules) && i < maxBranchRules; i++ {
		rule := live.Rules[i]
		if err := e.addRule(rule, live.RulesetLabel(rule)); err != nil {
			return nil, fmt.Errorf("read rule %q of %s: %w", rule.Type, live.RulesetLabel(rule), err)
		}
	}
	if live.Legacy != nil {
		e.addLegacy(live.Legacy)
	}
	return e, nil
}

// addRule adds one active ruleset rule, enforced by the mechanism by.
func (e *liveEnforcement) addRule(rule LiveBranchRule, by string) error {
	switch rule.Type {
	case "pull_request":
		var params pullRequestParameters
		if err := decodeRuleParameters(rule.Parameters, &params); err != nil {
			return err
		}
		e.addReviews(params.RequiredApprovingReviewCount, params.DismissStaleReviewsOnPush, params.RequireCodeOwnerReview, by)
	case "required_status_checks":
		return e.addStatusCheckRule(rule.Parameters, by)
	default:
		if flag := e.ruleFlag(rule.Type); flag != nil {
			flag.set(by)
		}
	}
	return nil
}

// addStatusCheckRule adds the checks a required_status_checks rule requires, enforced by by.
func (e *liveEnforcement) addStatusCheckRule(raw json.RawMessage, by string) error {
	var params statusCheckParameters
	if err := decodeRuleParameters(raw, &params); err != nil {
		return err
	}
	if len(params.RequiredStatusChecks) > maxRulesetContexts {
		return fmt.Errorf("required status checks exceed %d contexts", maxRulesetContexts)
	}
	for i := 0; i < len(params.RequiredStatusChecks) && i < maxRulesetContexts; i++ {
		e.addCheck(params.RequiredStatusChecks[i].Context, by)
	}
	return nil
}

// ruleFlag returns the requirement a parameterless rule type switches on, or nil for a rule
// type the declared policy does not describe.
func (e *liveEnforcement) ruleFlag(ruleType string) *liveFlag {
	switch ruleType {
	case "required_signatures":
		return &e.signatures
	case "required_linear_history":
		return &e.linearHistory
	case "deletion":
		return &e.deletionBlocked
	case "non_fast_forward":
		return &e.forcePushBlocked
	default:
		return nil
	}
}

// addReviews adds a pull request requirement with its review settings, enforced by by.
func (e *liveEnforcement) addReviews(count int, dismissStale, codeOwner bool, by string) {
	e.pullRequest.set(by)
	e.reviews.raise(count, by)
	if dismissStale {
		e.dismissStale.set(by)
	}
	if codeOwner {
		e.codeOwner.set(by)
	}
}

// addCheck records that by requires the status check context.
func (e *liveEnforcement) addCheck(context, by string) {
	if context != "" {
		e.checks[context] = appendMissing(e.checks[context], []string{by})
	}
}

// addLegacy adds the legacy protection object. It blocks deletion and force pushes unless it
// allows them, and requires what its present settings switch on.
func (e *liveEnforcement) addLegacy(p *LegacyBranchProtection) {
	by := LegacyProtectionMechanism
	if r := p.RequiredPullRequestReviews; r != nil {
		e.addReviews(r.RequiredApprovingReviewCount, r.DismissStaleReviews, r.RequireCodeOwnerReviews, by)
	}
	if p.RequiredSignatures.enabled() {
		e.signatures.set(by)
	}
	if p.RequiredLinearHistory.enabled() {
		e.linearHistory.set(by)
	}
	if !p.AllowDeletions.enabled() {
		e.deletionBlocked.set(by)
	}
	if !p.AllowForcePushes.enabled() {
		e.forcePushBlocked.set(by)
	}
	if p.RequiredStatusChecks != nil {
		e.addLegacyStatusChecks(p.RequiredStatusChecks, by)
	}
}

// addLegacyStatusChecks adds the checks a legacy protection object requires, by name and by
// check entry alike, enforced by by.
func (e *liveEnforcement) addLegacyStatusChecks(c *LegacyStatusChecks, by string) {
	for i := 0; i < len(c.Contexts) && i < maxRulesetContexts; i++ {
		e.addCheck(c.Contexts[i], by)
	}
	for i := 0; i < len(c.Checks) && i < maxRulesetContexts; i++ {
		e.addCheck(c.Checks[i].Context, by)
	}
}

// decodeRuleParameters decodes a rule's parameters into out; a rule without any keeps out zero.
func decodeRuleParameters(raw json.RawMessage, out any) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("parse rule parameters: %w", err)
	}
	return nil
}

// EvaluateBranchProtection compares what live enforces with the resolved branch protection
// policy, property by property, as praetor renders the policy into its ruleset
// (RenderRepositoryRuleset): pull requests with the effective approving review count and
// code-owner review (config.BranchProtectionPolicy.EffectiveReviewRequirements), stale review
// dismissal, signed commits, linear history, deletion and force pushes blocked, and the
// required status checks contexts. Every mechanism counts, since GitHub enforces their union;
// a branch nothing protects is drift on every declared property, never an error. When the
// repository has the ruleset praetor writes, its enforcement is compared too
// (rulesetEnforcementFinding).
func EvaluateBranchProtection(policy config.BranchProtectionPolicy, contexts []string, live *LiveBranchProtection) ([]ProtectionFinding, error) {
	if live == nil {
		return nil, errors.New("evaluate branch protection: no live protection was read")
	}
	reviews, codeOwner, err := policy.EffectiveReviewRequirements()
	if err != nil {
		return nil, fmt.Errorf("evaluate branch protection: %w", err)
	}
	if len(contexts) > maxRulesetContexts {
		return nil, fmt.Errorf("evaluate branch protection: required status checks exceed %d contexts", maxRulesetContexts)
	}
	e, err := collectEnforcement(live)
	if err != nil {
		return nil, fmt.Errorf("evaluate branch protection: %w", err)
	}
	declaredReviews := strconv.Itoa(reviews)
	if policy.ReviewMode == config.BranchReviewModeSingleMaintainer {
		declaredReviews += " (review_mode single_maintainer)"
	}
	findings := []ProtectionFinding{
		flagFinding("Pull requests", true, e.pullRequest),
		countFinding("Approving reviews", reviews, declaredReviews, e.reviews),
		flagFinding("Code owner review", codeOwner, e.codeOwner),
		flagFinding("Dismiss stale reviews", policy.DismissStaleReviews, e.dismissStale),
		flagFinding("Signed commits", policy.RequireSignedCommits, e.signatures),
		flagFinding("Linear history", policy.EnforceLinearHistory, e.linearHistory),
		flagFinding("Deletion blocked", true, e.deletionBlocked),
		flagFinding("Force pushes blocked", true, e.forcePushBlocked),
		checksFinding(contexts, e.checks),
	}
	if finding, compared := rulesetEnforcementFinding(live); compared {
		findings = append(findings, finding)
	}
	prFindings, err := evaluatePullRequestRule(policy.AllowedMergeMethods, policy.RequireExtraApprovalForUnattributedChanges, live)
	if err != nil {
		return nil, fmt.Errorf("evaluate branch protection: %w", err)
	}
	findings = append(findings, prFindings...)
	return append(findings, MergeQueueRulesetFindings(policy, live)...), nil
}

// rulesetEnforcementFinding compares the enforcement of the ruleset praetor writes
// (RepositoryRulesetName) with the active enforcement it renders. GitHub applies no rule of a
// ruleset in evaluate or disabled enforcement, so every property that ruleset carries reads as
// not enforced, and this finding names why. A repository without that ruleset, protected by
// other rulesets or the legacy protection object alone, and a listing that does not report the
// enforcement, are not compared on it.
func rulesetEnforcementFinding(live *LiveBranchProtection) (ProtectionFinding, bool) {
	id, enforcement, found := live.managedRuleset()
	if !found || enforcement == "" {
		return ProtectionFinding{}, false
	}
	label := live.RulesetLabel(LiveBranchRule{RulesetID: id})
	finding := ProtectionFinding{Property: "Ruleset enforcement", Declared: rulesetEnforcementActive,
		Live: enforcement + " (" + label + ")", Verdict: ProtectionDrift}
	if enforcement == rulesetEnforcementActive {
		finding.Live, finding.EnforcedBy, finding.Verdict = enforcement, []string{label}, ProtectionEnforced
	}
	return finding, true
}

// flagFinding compares an on-or-off requirement.
func flagFinding(property string, declared bool, live liveFlag) ProtectionFinding {
	finding := ProtectionFinding{Property: property, Declared: "not required", Live: "not enforced", EnforcedBy: live.by}
	if declared {
		finding.Declared = "required"
	}
	if live.on {
		finding.Live = "required"
	}
	switch {
	case declared && live.on:
		finding.Verdict = ProtectionEnforced
	case declared:
		finding.Verdict = ProtectionDrift
	case live.on:
		finding.Verdict = ProtectionStricter
	default:
		finding.Verdict = ProtectionNotRequired
	}
	return finding
}

// countFinding compares the approving review count; declaredText is how the declaration reads.
func countFinding(property string, declared int, declaredText string, live liveCount) ProtectionFinding {
	finding := ProtectionFinding{Property: property, Declared: declaredText, Live: strconv.Itoa(live.n), EnforcedBy: live.by}
	switch {
	case live.n < declared:
		finding.Verdict = ProtectionDrift
	case live.n > declared:
		finding.Verdict = ProtectionStricter
	case declared == 0:
		finding.Verdict = ProtectionNotRequired
	default:
		finding.Verdict = ProtectionEnforced
	}
	return finding
}

// checksFinding compares the required status checks: every declared context must be required
// by some mechanism. A live check the declaration does not list is not judged here; sync
// --remote reports the checks it leaves off itself.
func checksFinding(contexts []string, live map[string][]string) ProtectionFinding {
	var missing, by []string
	for i := 0; i < len(contexts) && i < maxRulesetContexts; i++ {
		mechanisms, required := live[contexts[i]]
		if !required {
			missing = append(missing, contexts[i])
			continue
		}
		by = appendMissing(by, mechanisms)
	}
	finding := ProtectionFinding{
		Property:   "Required status checks",
		Declared:   strconv.Itoa(len(contexts)),
		Live:       fmt.Sprintf("%d of %d required", len(contexts)-len(missing), len(contexts)),
		EnforcedBy: by,
		Verdict:    ProtectionEnforced,
	}
	switch {
	case len(missing) > 0:
		finding.Live += "; missing: " + strings.Join(missing, ", ")
		finding.Verdict = ProtectionDrift
	case len(contexts) == 0:
		finding.Declared, finding.Live = "none", "not compared"
		finding.Verdict = ProtectionNotRequired
	}
	return finding
}

// ProtectionDrifts returns the properties of findings the branch does not enforce as declared.
func ProtectionDrifts(findings []ProtectionFinding) []string {
	var drifted []string
	for i := 0; i < len(findings) && i < maxRulesetRules; i++ {
		if findings[i].Verdict == ProtectionDrift {
			drifted = append(drifted, findings[i].Property)
		}
	}
	return drifted
}

// maxPRParams bounds the parameters evaluated on a pull_request rule (HISS-02).
const maxPRParams = 32

// findLivePullRequestRule returns the active pull_request rule of the managed ruleset, or the
// first active pull_request rule found if no managed ruleset applies.
func findLivePullRequestRule(live *LiveBranchProtection) (*LiveBranchRule, bool) {
	if live == nil {
		return nil, false
	}
	managedID, _, hasManaged := live.managedRuleset()
	if hasManaged {
		for i := 0; i < len(live.Rules) && i < maxBranchRules; i++ {
			if live.Rules[i].Type == "pull_request" && live.Rules[i].RulesetID == managedID {
				return &live.Rules[i], true
			}
		}
	}
	for i := 0; i < len(live.Rules) && i < maxBranchRules; i++ {
		if live.Rules[i].Type == "pull_request" {
			return &live.Rules[i], true
		}
	}
	return nil, false
}

// evaluatePullRequestRule evaluates the rendered pull_request parameters against the live
// ruleset rule, reporting findings for declared/rendered parameters and drift on live parameters
// that differ from forge defaults.
func evaluatePullRequestRule(allowedMethods []string, extraApproval *bool, live *LiveBranchProtection) ([]ProtectionFinding, error) {
	liveRule, hasLiveRule := findLivePullRequestRule(live)
	if !hasLiveRule {
		return evaluateUnenforcedPRParams(allowedMethods, extraApproval), nil
	}
	var liveParams map[string]any
	if err := decodeRuleParameters(liveRule.Parameters, &liveParams); err != nil {
		return nil, err
	}
	label := live.RulesetLabel(*liveRule)
	renderedFindings := compareRenderedPRParams(allowedMethods, extraApproval, liveParams, label)
	liveOnlyFindings := compareLiveOnlyPRParams(allowedMethods, extraApproval, liveParams, label)
	return append(renderedFindings, liveOnlyFindings...), nil
}

// evaluateUnenforcedPRParams reports drift for declared PR parameters when no live pull_request
// rule applies to the branch.
func evaluateUnenforcedPRParams(allowedMethods []string, extraApproval *bool) []ProtectionFinding {
	var findings []ProtectionFinding
	if len(allowedMethods) > 0 {
		findings = append(findings, ProtectionFinding{
			Property: "allowed_merge_methods",
			Declared: formatMethods(allowedMethods),
			Live:     "not enforced",
			Verdict:  ProtectionDrift,
		})
	}
	if extraApproval != nil {
		findings = append(findings, ProtectionFinding{
			Property: "require_extra_approval_for_unattributed_changes",
			Declared: strconv.FormatBool(*extraApproval),
			Live:     "not enforced",
			Verdict:  ProtectionDrift,
		})
	}
	return findings
}

// isSubset reports whether every key in sub is also in super.
func isSubset(sub, super map[string]bool) bool {
	i := 0
	for k := range sub {
		if i >= maxPRParams {
			break
		}
		i++
		if !super[k] {
			return false
		}
	}
	return true
}

// compareRenderedPRParams compares parameters rendered by praetorctl with live values.
func compareRenderedPRParams(allowedMethods []string, extraApproval *bool, liveParams map[string]any, label string) []ProtectionFinding {
	var findings []ProtectionFinding
	if len(allowedMethods) > 0 {
		declaredSet := toSet(allowedMethods)
		liveSet := extractStringSet(liveParams["allowed_merge_methods"])
		var verdict ProtectionVerdict
		switch {
		case maps.Equal(declaredSet, liveSet):
			verdict = ProtectionEnforced
		case pullRequestParameterStricter("allowed_merge_methods", liveParams["allowed_merge_methods"], allowedMethods):
			verdict = ProtectionStricter
		default:
			verdict = ProtectionDrift
		}
		findings = append(findings, ProtectionFinding{
			Property:   "allowed_merge_methods",
			Declared:   formatMethods(allowedMethods),
			Live:       formatMethods(sortedKeys(liveSet)),
			EnforcedBy: []string{label},
			Verdict:    verdict,
		})
	}
	if extraApproval != nil {
		declared := *extraApproval
		live := toBool(liveParams["require_extra_approval_for_unattributed_changes"])
		var verdict ProtectionVerdict
		switch {
		case declared == live:
			verdict = ProtectionEnforced
		case pullRequestParameterStricter("require_extra_approval_for_unattributed_changes", live, declared):
			verdict = ProtectionStricter
		default:
			verdict = ProtectionDrift
		}
		findings = append(findings, ProtectionFinding{
			Property:   "require_extra_approval_for_unattributed_changes",
			Declared:   strconv.FormatBool(declared),
			Live:       strconv.FormatBool(live),
			EnforcedBy: []string{label},
			Verdict:    verdict,
		})
	}
	findings = append(findings, compareStaticPRParams(liveParams, label)...)
	return findings
}

// compareStaticPRParams checks static pull_request parameters praetor renders (require_last_push_approval: false,
// required_review_thread_resolution: true) against live values.
func compareStaticPRParams(liveParams map[string]any, label string) []ProtectionFinding {
	var findings []ProtectionFinding
	if pullRequestParameterStricter("require_last_push_approval", liveParams["require_last_push_approval"], false) {
		findings = append(findings, ProtectionFinding{
			Property:   "require_last_push_approval",
			Declared:   "false",
			Live:       "true",
			EnforcedBy: []string{label},
			Verdict:    ProtectionStricter,
		})
	}
	if raw, ok := liveParams["required_review_thread_resolution"]; ok && !toBool(raw) {
		findings = append(findings, ProtectionFinding{
			Property:   "required_review_thread_resolution",
			Declared:   "true",
			Live:       "false",
			EnforcedBy: []string{label},
			Verdict:    ProtectionDrift,
		})
	}
	return findings
}

// liveOnlyParamVerdict classifies a live parameter absent from the render: stricter if live
// tightens beyond forge defaults, drift otherwise.
func liveOnlyParamVerdict(key string, val any) ProtectionVerdict {
	switch key {
	case "allowed_merge_methods":
		defaultSet := []string{"merge", "squash", "rebase"}
		if pullRequestParameterStricter(key, val, defaultSet) {
			return ProtectionStricter
		}
	case "require_extra_approval_for_unattributed_changes":
		if pullRequestParameterStricter(key, val, false) {
			return ProtectionStricter
		}
	}
	return ProtectionDrift
}

// compareLiveOnlyPRParams checks live parameters not carried by the render against forge defaults.
func compareLiveOnlyPRParams(allowedMethods []string, extraApproval *bool, liveParams map[string]any, label string) []ProtectionFinding {
	keys := make([]string, 0, len(liveParams))
	for k := range liveParams {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	var findings []ProtectionFinding
	for i := 0; i < len(keys) && i < maxPRParams; i++ {
		key := keys[i]
		if isCarriedByRender(key, allowedMethods, extraApproval) {
			continue
		}
		val := liveParams[key]
		if isLiveOnlyDefault(key, val) {
			continue
		}
		findings = append(findings, ProtectionFinding{
			Property:   key,
			Declared:   declaredDefault(key),
			Live:       formatLiveValue(key, val),
			EnforcedBy: []string{label},
			Verdict:    liveOnlyParamVerdict(key, val),
		})
	}
	return findings
}

// isCarriedByRender reports whether key is a pull_request parameter rendered under policy.
func isCarriedByRender(key string, allowedMethods []string, extraApproval *bool) bool {
	switch key {
	case "required_approving_review_count", "dismiss_stale_reviews_on_push",
		"require_code_owner_review", "require_last_push_approval",
		"required_review_thread_resolution":
		return true
	case "allowed_merge_methods":
		return len(allowedMethods) > 0
	case "require_extra_approval_for_unattributed_changes":
		return extraApproval != nil
	default:
		return false
	}
}

// isLiveOnlyDefault reports whether val matches the GitHub default for an un-rendered parameter.
func isLiveOnlyDefault(key string, val any) bool {
	switch key {
	case "allowed_merge_methods":
		defaultSet := map[string]bool{"merge": true, "squash": true, "rebase": true}
		return maps.Equal(extractStringSet(val), defaultSet)
	case "dismissal_restriction":
		return isDefaultDismissalRestriction(val)
	case "required_reviewers":
		items, ok := val.([]any)
		return !ok || len(items) == 0
	default:
		return isDefaultScalar(val)
	}
}

func isDefaultDismissalRestriction(val any) bool {
	obj, ok := val.(map[string]any)
	if !ok || obj == nil {
		return true
	}
	if enabled, okEnabled := obj["enabled"].(bool); okEnabled && enabled {
		return false
	}
	if actors, okActors := obj["allowed_actors"].([]any); okActors && len(actors) > 0 {
		return false
	}
	return true
}

func isDefaultScalar(val any) bool {
	if b, ok := val.(bool); ok {
		return !b
	}
	if n, err := jsonInteger(val); err == nil {
		return n == 0
	}
	return val == nil
}

func declaredDefault(key string) string {
	switch key {
	case "allowed_merge_methods":
		return "[merge, squash, rebase]"
	case "dismissal_restriction":
		return "enabled: false, allowed_actors: []"
	case "required_reviewers":
		return "[]"
	default:
		return "false"
	}
}

func formatLiveValue(key string, val any) string {
	switch key {
	case "allowed_merge_methods":
		return formatMethods(sortedKeys(extractStringSet(val)))
	default:
		return fmt.Sprintf("%v", val)
	}
}

func toSet(items []string) map[string]bool {
	set := make(map[string]bool, len(items))
	for i := 0; i < len(items) && i < maxPRParams; i++ {
		set[items[i]] = true
	}
	return set
}

func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return sortMethods(keys)
}

func sortMethods(methods []string) []string {
	order := map[string]int{"merge": 1, "squash": 2, "rebase": 3}
	sorted := slices.Clone(methods)
	slices.SortFunc(sorted, func(a, b string) int {
		return order[a] - order[b]
	})
	return sorted
}

func formatMethods(methods []string) string {
	sorted := sortMethods(methods)
	return "[" + strings.Join(sorted, ", ") + "]"
}

func extractStringSet(raw any) map[string]bool {
	set := make(map[string]bool, 4)
	switch items := raw.(type) {
	case []string:
		for i := 0; i < len(items) && i < maxPRParams; i++ {
			set[items[i]] = true
		}
	case []any:
		for i := 0; i < len(items) && i < maxPRParams; i++ {
			if s, ok := items[i].(string); ok {
				set[s] = true
			}
		}
	}
	return set
}

func toBool(val any) bool {
	switch b := val.(type) {
	case bool:
		return b
	case string:
		return b == "true"
	default:
		return false
	}
}
