package forge

import (
	"encoding/json"
	"errors"
	"fmt"
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
	// ProtectionStricter means the branch enforces more than declared. sync --remote keeps it,
	// because it never narrows a live ruleset; lowering it is the operator's change.
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
// a branch nothing protects is drift on every declared property, never an error.
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
	return []ProtectionFinding{
		flagFinding("Pull requests", true, e.pullRequest),
		countFinding("Approving reviews", reviews, declaredReviews, e.reviews),
		flagFinding("Code owner review", codeOwner, e.codeOwner),
		flagFinding("Dismiss stale reviews", policy.DismissStaleReviews, e.dismissStale),
		flagFinding("Signed commits", policy.RequireSignedCommits, e.signatures),
		flagFinding("Linear history", policy.EnforceLinearHistory, e.linearHistory),
		flagFinding("Deletion blocked", true, e.deletionBlocked),
		flagFinding("Force pushes blocked", true, e.forcePushBlocked),
		checksFinding(contexts, e.checks),
	}, nil
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
