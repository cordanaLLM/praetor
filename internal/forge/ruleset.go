package forge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/util"
	"gopkg.in/yaml.v3"
)

const (
	maxRulesetContexts = 64 * 64
	maxRulesetRules    = 64
)

type statusRuleset struct {
	Rules []struct {
		Type       string `json:"type"`
		Parameters struct {
			RequiredStatusChecks []struct {
				Context string `json:"context"`
			} `json:"required_status_checks"`
		} `json:"parameters"`
	} `json:"rules"`
}

// RulesetRequiresStatusContext inspects only semantic required-status-check
// entries. Unrelated metadata or prose containing the same text is ignored.
func RulesetRequiresStatusContext(data []byte, target string) (bool, error) {
	if target == "" || strings.TrimSpace(target) != target {
		return false, fmt.Errorf("required status context selector must be nonempty and trimmed")
	}
	ruleset, err := parseStatusRuleset(data)
	if err != nil {
		return false, err
	}
	for ruleIndex := 0; ruleIndex < len(ruleset.Rules) && ruleIndex < maxRulesetRules; ruleIndex++ {
		rule := ruleset.Rules[ruleIndex]
		if rule.Type == "required_status_checks" {
			found, findErr := statusChecksContain(rule.Parameters.RequiredStatusChecks, target)
			if findErr != nil || found {
				return found, findErr
			}
		}
	}
	return false, nil
}

func parseStatusRuleset(data []byte) (*statusRuleset, error) {
	if !json.Valid(data) {
		return nil, fmt.Errorf("ruleset must be a single valid JSON document")
	}
	var ruleset statusRuleset
	if err := json.Unmarshal(data, &ruleset); err != nil {
		return nil, fmt.Errorf("parse ruleset status contexts: %w", err)
	}
	if len(ruleset.Rules) > maxRulesetRules {
		return nil, fmt.Errorf("ruleset exceeds %d rules", maxRulesetRules)
	}
	return &ruleset, nil
}

func statusChecksContain(checks []struct {
	Context string `json:"context"`
}, target string) (bool, error) {
	if len(checks) > maxRulesetContexts {
		return false, fmt.Errorf("required status checks exceed %d contexts", maxRulesetContexts)
	}
	for checkIndex := 0; checkIndex < len(checks) && checkIndex < maxRulesetContexts; checkIndex++ {
		if checks[checkIndex].Context == target {
			return true, nil
		}
	}
	return false, nil
}

// RepositoryRulesetName names the praetor branch protection ruleset, in
// .github/rulesets/main.json and on the forge alike. Like the file name, it is an identifier a
// live ruleset is matched by, and it stays the same whatever the default branch is.
const RepositoryRulesetName = "praetor-main-protection"

// rulesetEnforcementActive is the enforcement praetor renders its ruleset with, the only one
// under which GitHub applies the ruleset's rules.
const rulesetEnforcementActive = "active"

// RepositoryRulesetRefs returns the refs the praetor ruleset protects for a repository whose
// default branch (RepositoryDefaultBranch) is branch: that branch and every lts-* branch, the
// release line .config/flavors.yaml tracks. The local file and a remote sync share it, so neither
// narrows the other.
func RepositoryRulesetRefs(branch string) []string {
	return []string{"refs/heads/" + branch, "refs/heads/lts-*"}
}

// RenderRepositoryRuleset renders local branch protection of the default branch branch from the
// selected policy and actual required check contexts. An empty selection omits the status rule.
// A branch config.ValidBranchName refuses is an error.
func RenderRepositoryRuleset(branch string, policy config.BranchProtectionPolicy, contexts []string) ([]byte, error) {
	if !config.ValidBranchName(branch) {
		return nil, fmt.Errorf("ruleset default branch %q is not a branch name", branch)
	}
	doc, err := protectionRuleset(RepositoryRulesetName, RepositoryRulesetRefs(branch), policy, contexts, true)
	if err != nil {
		return nil, err
	}
	return json.MarshalIndent(doc, "", "  ")
}

// RepositoryRulesetPath is where a repository carries the praetor branch protection ruleset,
// relative to its root and in slash form.
const RepositoryRulesetPath = ".github/rulesets/main.json"

// RenderRulesetForRepository renders the ruleset for the repository at repoPath under policy,
// protecting its default branch (RepositoryDefaultBranch). Its required status checks are the
// workflow jobs RequiredStatusContextsPlanned selects from the workflows present now with planned
// applied over them. A caller that writes its workflows first passes nil; a dry run, which writes
// none, passes the ones its run would write or remove, so it renders what that run writes. The
// contexts come back beside the ruleset for a caller that reports or validates against them.
//
// Adoption's branch-ruleset step and flavor apply both write this rendering, so a repository
// either one scaffolded carries the file sync and the audit validate (ValidateRepositoryRuleset).
func RenderRulesetForRepository(ctx context.Context, repoPath string, policy config.BranchProtectionPolicy, planned map[string][]byte) ([]byte, []string, error) {
	branch, err := RepositoryDefaultBranch(ctx, repoPath, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("render %s: %w", RepositoryRulesetPath, err)
	}
	contexts, err := RequiredStatusContextsPlanned(ctx, repoPath, planned)
	if err != nil {
		return nil, nil, err
	}
	data, err := RenderRepositoryRuleset(branch, policy, contexts)
	if err != nil {
		return nil, nil, fmt.Errorf("render %s: %w", RepositoryRulesetPath, err)
	}
	return data, contexts, nil
}

// RulesetBaseline is the repository state a ruleset rendering is rendered from: the default
// branch (RepositoryDefaultBranch), the effective branch protection policy
// (RepositoryBranchPolicy) and the required status contexts of the workflows present
// (RequiredStatusContexts).
type RulesetBaseline struct {
	Branch   string
	Policy   config.BranchProtectionPolicy
	Contexts []string
}

// ReadRulesetBaseline reads the baseline of the repository at repoPath as it stands. A writer
// reads it before it changes the repository, so that PriorRulesetDigests can tell the ruleset
// that was current then from one an adopter edited.
func ReadRulesetBaseline(ctx context.Context, repoPath string) (RulesetBaseline, error) {
	// The policy is read first: a manifest that does not load fails as the policy it declares.
	policy, err := RepositoryBranchPolicy(ctx, repoPath)
	if err != nil {
		return RulesetBaseline{}, err
	}
	branch, err := RepositoryDefaultBranch(ctx, repoPath, nil)
	if err != nil {
		return RulesetBaseline{}, fmt.Errorf("read the default branch for %s: %w", RepositoryRulesetPath, err)
	}
	contexts, err := RequiredStatusContexts(ctx, repoPath)
	if err != nil {
		return RulesetBaseline{}, fmt.Errorf("read the workflow checks for %s: %w", RepositoryRulesetPath, err)
	}
	return RulesetBaseline{Branch: branch, Policy: policy, Contexts: contexts}, nil
}

// RepositoryBranchPolicy is the branch protection the repository at repoPath renders its ruleset
// under: its effective policy as sync and the audit resolve it (config.ResolveRepositoryPolicy:
// the pinned profiles and facets with the manifest's overrides, or defaults plus overrides before
// a lock exists). A repository without .standards.yaml declares nothing to resolve, so the
// built-in default applies. A policy that does not resolve has no stand-in and is an error.
func RepositoryBranchPolicy(ctx context.Context, repoPath string) (config.BranchProtectionPolicy, error) {
	policy, _, err := config.ResolveRepositoryPolicy(ctx, filepath.Join(repoPath, config.ManifestFileName), nil)
	if err != nil {
		return config.BranchProtectionPolicy{}, fmt.Errorf("resolve the effective policy for %s: %w", RepositoryRulesetPath, err)
	}
	if policy == nil {
		return config.DefaultPolicy().BranchProtection, nil
	}
	return policy.BranchProtection, nil
}

// PriorRulesetDigests returns, for a scaffold's earlier-text lookup (util.LookupCanonicalText),
// the digest (util.CanonicalTextDigest) of the ruleset RenderRepositoryRuleset renders from
// baseline, the repository as it stood before a writer changed it. That rendering is the ruleset
// sync and the audit accepted then, so a file holding it in one consistent line-ending style is
// Praetor's and nobody edited it: adoption and flavor apply refresh it to current, the rendering
// they write now, without --force.
//
// When baseline's default branch is not FallbackDefaultBranch, the map also holds the rendering
// of the same policy and workflows for FallbackDefaultBranch: every Praetor before it read the
// default branch rendered main whatever the repository's default branch was, so that file is
// Praetor's too and is refreshed to the default branch. A rendering is left out when it is
// current itself, which needs no refresh, or cannot be rendered; the map is then empty.
//
// Nothing is read back from the file: any other ruleset, a rendering with one value edited (a
// review count, a signature rule, a status check) as much as an operator's own, matches no digest
// and keeps the --force contract.
func PriorRulesetDigests(baseline RulesetBaseline, current []byte) map[string]string {
	priors := make(map[string]string, 2)
	addPriorRuleset(priors, baseline.Branch, baseline, current,
		"the ruleset of the repository's policy and workflows before this run")
	if baseline.Branch != FallbackDefaultBranch {
		addPriorRuleset(priors, FallbackDefaultBranch, baseline, current,
			"the ruleset of the repository's policy and workflows before this run, for main as Praetor rendered it before it read the default branch")
	}
	if len(priors) == 0 {
		return nil
	}
	return priors
}

// addPriorRuleset adds to priors, under label, the digest of the ruleset baseline's policy and
// contexts render for branch, unless that rendering is current or cannot be rendered.
func addPriorRuleset(priors map[string]string, branch string, baseline RulesetBaseline, current []byte, label string) {
	prior, err := RenderRepositoryRuleset(branch, baseline.Policy, baseline.Contexts)
	if err != nil {
		return
	}
	if same, err := util.CanonicalTextEquivalent(prior, current); err != nil || same {
		return
	}
	digest, _, err := util.CanonicalTextDigest(prior)
	if err != nil {
		return
	}
	priors[digest] = label
}

// ErrRulesetDrift reports a committed ruleset whose content differs from the one the declared
// branch protection policy renders.
var ErrRulesetDrift = errors.New("ruleset differs from declared branch protection policy; review the existing file before reconciliation")

// ValidateRepositoryRuleset checks a committed ruleset against the one RenderRepositoryRuleset
// produces for the default branch branch, policy and contexts. It is the single content check
// behind sync, the branch protection audit and the documentation audit; the audit used to check
// only that the file existed, so "{}" and a ruleset with zero approvals and no signature rule
// both passed it.
//
// The data must be JSON syntax, and is then parsed through YAML because yaml.v3 rejects
// duplicate keys, nested JSON objects included. Comparing parsed documents ignores whitespace
// and object key order while preserving every declared rule, parameter and condition.
func ValidateRepositoryRuleset(data []byte, branch string, policy config.BranchProtectionPolicy, contexts []string) error {
	if !json.Valid(data) {
		return errors.New("ruleset must be a single valid JSON document")
	}
	var observed map[string]any
	if err := yaml.Unmarshal(data, &observed); err != nil {
		return fmt.Errorf("parse ruleset: %w", err)
	}
	expectedBytes, err := RenderRepositoryRuleset(branch, policy, contexts)
	if err != nil {
		return err
	}
	var expected map[string]any
	if err := yaml.Unmarshal(expectedBytes, &expected); err != nil {
		return fmt.Errorf("parse rendered ruleset: %w", err)
	}
	if !reflect.DeepEqual(observed, expected) {
		return ErrRulesetDrift
	}
	return nil
}

// protectionRuleset renders the praetor ruleset named name over refs under policy, requiring
// contexts, with the bypass actors policy declares (rulesetBypassActors). The local file and a
// ruleset sync --remote writes are both rendered here.
func protectionRuleset(name string, refs []string, policy config.BranchProtectionPolicy, contexts []string, strict bool) (map[string]any, error) {
	doc, err := protectionDocument(name, refs, policy, contexts, strict)
	if err != nil {
		return nil, err
	}
	if actors := rulesetBypassActors(policy); actors != nil {
		doc["bypass_actors"] = actors
	}
	return doc, nil
}

// repositoryAdminRoleID is the actor_id of the repository admin role in a ruleset's
// bypass_actors entry of actor_type RepositoryRole.
const repositoryAdminRoleID = 5

// rulesetBypassActors returns the bypass_actors of a ruleset rendered under policy. Under review
// mode single_maintainer it is the repository admin role in bypass mode pull_request: the one
// maintainer can merge a pull request whose rules cannot be met, such as a required check no run
// reports, and still cannot push to the branch past them (#76). Every other mode renders none, so
// every rule binds every actor. A live ruleset keeps its own bypass actors (mergeRuleset), so this
// entry reaches GitHub only in a ruleset sync --remote creates.
func rulesetBypassActors(policy config.BranchProtectionPolicy) []map[string]any {
	if policy.ReviewMode != config.BranchReviewModeSingleMaintainer {
		return nil
	}
	return []map[string]any{{"actor_id": repositoryAdminRoleID, "actor_type": "RepositoryRole", "bypass_mode": "pull_request"}}
}

// protectionDocument is protectionRuleset without its bypass actors: the name, target,
// enforcement, refs and rules.
func protectionDocument(name string, refs []string, policy config.BranchProtectionPolicy, contexts []string, strict bool) (map[string]any, error) {
	reviewCount, requireCodeOwner, err := policy.EffectiveReviewRequirements()
	if err != nil {
		return nil, err
	}
	if err := validateRulesetInputs(contexts); err != nil {
		return nil, err
	}
	rules := protectionRules(policy, reviewCount, requireCodeOwner)
	if len(contexts) > 0 {
		checks := make([]map[string]string, 0, len(contexts))
		for i := 0; i < len(contexts) && i < maxRulesetContexts; i++ {
			checks = append(checks, map[string]string{"context": contexts[i]})
		}
		rules = append(rules, map[string]any{"type": "required_status_checks", "parameters": map[string]any{
			"strict_required_status_checks_policy": strict,
			"required_status_checks":               checks,
		}})
	}
	return map[string]any{
		"name": name, "target": "branch", "enforcement": rulesetEnforcementActive,
		"conditions": map[string]any{"ref_name": map[string]any{"include": refs, "exclude": []string{}}},
		"rules":      rules,
	}, nil
}

func validateRulesetInputs(contexts []string) error {
	if len(contexts) > maxRulesetContexts {
		return fmt.Errorf("required status checks exceed %d contexts", maxRulesetContexts)
	}
	seen := make(map[string]bool, len(contexts))
	for i := 0; i < len(contexts) && i < maxRulesetContexts; i++ {
		name := contexts[i]
		if name == "" || strings.TrimSpace(name) != name || seen[name] || strings.ContainsAny(name, "\r\n\x00") {
			return errors.New("required status check contexts must be nonempty, trimmed and unique")
		}
		seen[name] = true
	}
	return nil
}

func protectionRules(policy config.BranchProtectionPolicy, reviewCount int, requireCodeOwner bool) []map[string]any {
	rules := []map[string]any{{"type": "deletion"}, {"type": "non_fast_forward"}}
	if policy.EnforceLinearHistory {
		rules = append(rules, map[string]any{"type": "required_linear_history"})
	}
	if policy.RequireSignedCommits {
		rules = append(rules, map[string]any{"type": "required_signatures"})
	}
	return append(rules, map[string]any{"type": "pull_request", "parameters": map[string]any{
		"required_approving_review_count":   reviewCount,
		"dismiss_stale_reviews_on_push":     policy.DismissStaleReviews,
		"require_code_owner_review":         requireCodeOwner,
		"require_last_push_approval":        false,
		"required_review_thread_resolution": true,
	}})
}
