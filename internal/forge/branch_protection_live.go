package forge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	// legacyProtectionAbsent is the message GitHub answers GET .../branches/{branch}/protection
	// with when the branch has no legacy protection object.
	legacyProtectionAbsent = "Branch not protected"
	// legacyBranchAbsent is the message of the same GET when the repository has no such branch
	// yet, such as a default branch not pushed yet: a missing branch carries no legacy
	// protection object, while a ruleset that targets its name already applies to it. Any
	// other 404, or a 403, does not say the branch is unprotected and is an error.
	legacyBranchAbsent = "Branch not found"
	// LegacyProtectionMechanism names the legacy branch protection object as the mechanism that
	// enforces a property, beside the rulesets LiveBranchProtection.RulesetLabel names.
	LegacyProtectionMechanism = "branch protection"
	// maxBranchRulePages bounds the listing of a branch's active rules (HISS-02): at most 1000.
	maxBranchRulePages = maxRulesetPages
	// maxBranchRules bounds every walk over a branch's active rules (HISS-02).
	maxBranchRules = maxBranchRulePages * issuesPerPage
)

// LiveBranchRule is one active rule GitHub enforces on a branch, as
// GET /repos/{owner}/{repo}/rules/branches/{branch} lists it: the rule and the ruleset it comes
// from. GitHub lists no rule of a ruleset in evaluate or disabled enforcement.
type LiveBranchRule struct {
	Type              string          `json:"type"`
	Parameters        json.RawMessage `json:"parameters,omitempty"`
	RulesetSourceType string          `json:"ruleset_source_type"`
	RulesetSource     string          `json:"ruleset_source"`
	RulesetID         int             `json:"ruleset_id"`
}

// EnabledSetting is a legacy protection setting GitHub reports as {"enabled": bool}.
type EnabledSetting struct {
	Enabled bool `json:"enabled"`
}

// enabled reports whether the setting is present and on.
func (s *EnabledSetting) enabled() bool {
	return s != nil && s.Enabled
}

// LegacyPullRequestReviews is the review requirement of a legacy protection object.
type LegacyPullRequestReviews struct {
	RequiredApprovingReviewCount int  `json:"required_approving_review_count"`
	DismissStaleReviews          bool `json:"dismiss_stale_reviews"`
	RequireCodeOwnerReviews      bool `json:"require_code_owner_reviews"`
}

// StatusCheckRef names one required status check.
type StatusCheckRef struct {
	Context string `json:"context"`
}

// LegacyStatusChecks is the status check requirement of a legacy protection object. GitHub
// reports each check in Checks and, deprecated, by name in Contexts.
type LegacyStatusChecks struct {
	Contexts []string         `json:"contexts"`
	Checks   []StatusCheckRef `json:"checks"`
}

// LegacyBranchProtection is the part of the legacy branch protection object
// (GET /repos/{owner}/{repo}/branches/{branch}/protection) the declared policy is compared with.
type LegacyBranchProtection struct {
	RequiredPullRequestReviews *LegacyPullRequestReviews `json:"required_pull_request_reviews"`
	RequiredSignatures         *EnabledSetting           `json:"required_signatures"`
	RequiredLinearHistory      *EnabledSetting           `json:"required_linear_history"`
	AllowForcePushes           *EnabledSetting           `json:"allow_force_pushes"`
	AllowDeletions             *EnabledSetting           `json:"allow_deletions"`
	RequiredStatusChecks       *LegacyStatusChecks       `json:"required_status_checks"`
}

// LiveBranchProtection is what GitHub enforces on one branch now, read from both of its
// protection mechanisms. A branch can be protected by rulesets, by the legacy protection
// object, or by both, and GitHub enforces the union; a reader of only one reports a branch the
// other protects as unprotected (#154).
type LiveBranchProtection struct {
	Branch string
	// Rules are the active rules of every ruleset that targets the branch, the repository's
	// own and those of its organisation alike.
	Rules []LiveBranchRule
	// RulesetNames maps a ruleset id to its name, to name the mechanism of a rule.
	RulesetNames map[int]string
	// Legacy is the legacy protection object, or nil when GitHub reports the branch has none.
	Legacy *LegacyBranchProtection
	// Missing reports that the repository on GitHub has no such branch yet. Its rulesets are
	// still read: GitHub lists the rules of every ruleset that targets the branch's name.
	Missing bool
}

// RulesetLabel names the ruleset rule comes from, such as ruleset "praetor-main-protection" #7,
// with its owner when the ruleset is not the repository's own.
func (l *LiveBranchProtection) RulesetLabel(rule LiveBranchRule) string {
	label := fmt.Sprintf("ruleset #%d", rule.RulesetID)
	if name := l.RulesetNames[rule.RulesetID]; name != "" {
		label = fmt.Sprintf("ruleset %q #%d", name, rule.RulesetID)
	}
	if rule.RulesetSourceType != "" && rule.RulesetSourceType != "Repository" {
		label += fmt.Sprintf(" of %s %s", strings.ToLower(rule.RulesetSourceType), rule.RulesetSource)
	}
	return label
}

// Mechanisms names every mechanism that protects the branch: each ruleset with an active rule
// on it, in the order GitHub listed them, then the legacy protection object when it has one.
// An empty list means nothing protects the branch.
func (l *LiveBranchProtection) Mechanisms() []string {
	var mechanisms []string
	for i := 0; i < len(l.Rules) && i < maxBranchRules; i++ {
		mechanisms = appendMissing(mechanisms, []string{l.RulesetLabel(l.Rules[i])})
	}
	if l.Legacy != nil {
		mechanisms = appendMissing(mechanisms, []string{LegacyProtectionMechanism})
	}
	return mechanisms
}

// ReadBranchProtection reads what GitHub enforces on branch from both mechanisms: the active
// rules of every ruleset that targets it (GET .../rules/branches/{branch}), named from the
// ruleset listing, and the legacy protection object (GET .../branches/{branch}/protection),
// where GitHub's "Branch not protected" answer means the branch has none and its "Branch not
// found" answer means the branch does not exist yet, so it has none either (Missing). It only
// reads.
func (g *GitHubDriver) ReadBranchProtection(ctx context.Context, branch string) (*LiveBranchProtection, error) {
	if err := g.Authenticate(ctx); err != nil {
		return nil, err
	}
	if branch == "" {
		return nil, errors.New("read branch protection: branch cannot be empty")
	}
	rules, err := g.branchRules(ctx, branch)
	if err != nil {
		return nil, fmt.Errorf("read the active rules of %s: %w", branch, err)
	}
	names := map[int]string{}
	if len(rules) > 0 {
		if names, err = g.rulesetNames(ctx); err != nil {
			return nil, fmt.Errorf("name the rulesets of %s: %w", branch, err)
		}
	}
	legacy, missing, err := g.legacyProtection(ctx, branch)
	if err != nil {
		return nil, fmt.Errorf("read the legacy branch protection of %s: %w", branch, err)
	}
	return &LiveBranchProtection{Branch: branch, Rules: rules, RulesetNames: names, Legacy: legacy, Missing: missing}, nil
}

// branchRules lists every active rule GitHub enforces on branch, page by page.
func (g *GitHubDriver) branchRules(ctx context.Context, branch string) ([]LiveBranchRule, error) {
	base, err := g.repoPath("rules/branches/" + url.PathEscape(branch))
	if err != nil {
		return nil, err
	}
	var rules []LiveBranchRule
	err = g.walkPages(ctx, base, "", "active branch rules", maxBranchRulePages, func(body []byte) (int, bool, error) {
		var page []LiveBranchRule
		if err := json.Unmarshal(body, &page); err != nil {
			return 0, false, fmt.Errorf("failed parsing active branch rules (raw: %q): %w", util.BodyPreview(body), err)
		}
		if len(page) > issuesPerPage {
			return 0, false, fmt.Errorf("branch rule response exceeds %d entries", issuesPerPage)
		}
		rules = append(rules, page...)
		return len(page), false, nil
	})
	if err != nil {
		return nil, err
	}
	return rules, nil
}

// rulesetNames maps the id of every ruleset the repository listing returns to its name.
func (g *GitHubDriver) rulesetNames(ctx context.Context) (map[int]string, error) {
	listPath, err := g.repoPath("rulesets")
	if err != nil {
		return nil, err
	}
	names := map[int]string{}
	err = g.walkRulesets(ctx, listPath, func(ruleset ghRulesetRaw) bool {
		names[ruleset.ID] = ruleset.Name
		return false
	})
	if err != nil {
		return nil, err
	}
	return names, nil
}

// legacyProtection reads the legacy protection object of branch. It returns nil when GitHub
// answers that the branch has none, and nil and true when GitHub has no such branch.
func (g *GitHubDriver) legacyProtection(ctx context.Context, branch string) (*LegacyBranchProtection, bool, error) {
	path, err := g.repoPath("branches/" + url.PathEscape(branch) + "/protection")
	if err != nil {
		return nil, false, err
	}
	body, status, err := g.sendRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, false, err
	}
	if status == http.StatusOK {
		var protection LegacyBranchProtection
		if err := json.Unmarshal(body, &protection); err != nil {
			return nil, false, fmt.Errorf("failed parsing branch protection (raw: %q): %w", util.BodyPreview(body), err)
		}
		return &protection, false, nil
	}
	if status == http.StatusNotFound {
		switch responseMessage(body) {
		case legacyProtectionAbsent:
			return nil, false, nil
		case legacyBranchAbsent:
			return nil, true, nil
		}
	}
	return nil, false, fmt.Errorf("unexpected status %d from GET %s: %s", status, path, util.BodyPreview(body))
}

// responseMessage returns the message field of a GitHub error body, or "" when it has none.
func responseMessage(body []byte) string {
	var answer struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &answer); err != nil {
		return ""
	}
	return answer.Message
}
