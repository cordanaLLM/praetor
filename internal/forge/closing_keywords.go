// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package forge

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

// A closing keyword next to a reference closes that issue or pull request when the text reaches
// the default branch, whatever the sentence around it says: on 2026-10-11 a squash body that read
// "nobody here closes #28 by hand" closed a bot's pull request on merge. GitHub documents the
// keywords and the reference forms in "Linking a pull request to an issue"; the keyword is
// case-insensitive and may carry a colon ("Closes: #10").
//
// closingKeywordRegex is the one reader of closing references (HISS-19): ParseClosingIssueNumbers
// reads closed issue numbers through it, and closingReferenceFindings checks commit messages and
// pull request bodies through it. Submatches: 1 the keyword; 2 the whole reference; 3-6 a URL's
// owner, repository, kind (issues, pull or pulls) and number; 7-9 a short reference's owner,
// repository and number; 10 the number of a GH-N reference.
var closingKeywordRegex = regexp.MustCompile(`(?i)\b(close[sd]?|fix(?:e[sd])?|resolve[sd]?)\b[ \t]*:?\s*(` +
	`https?://[^\s/]+/([a-z0-9][\w.-]*)/([\w.-]+)/(issues|pulls?)/(\d+)` +
	`|(?:([a-z0-9][\w.-]*)/)?([\w.-]+)?#(\d+)` +
	`|gh-(\d+))\b`)

// closingAllowMarker starts the line that marks one closing reference as intended: the author
// opened that pull request, or the reference is an issue the change is meant to close. The line
// carries the reference as the text writes it, compared without case: "Closes-own: #12". The
// keyword in it is followed by "-own:", not by a reference, so it closes nothing itself.
const closingAllowMarker = "Closes-own:"

// maxClosingReferences bounds the closing references one text is checked for (HISS-02). A text
// with more is refused, not checked in part.
const maxClosingReferences = 1000

// closingReference is one closing keyword and the reference it closes.
type closingReference struct {
	keyword string
	ref     string
	owner   string
	repo    string
	// pull is true for a pull request URL; every other form may name an issue or a pull
	// request, which share one number space.
	pull bool
	// local is true for a bare #N, a number in the repository the text belongs to.
	local  bool
	number int
	line   int
}

// findClosingReferences returns up to limit closing references of text, each with its 1-based
// line, and whether text holds more than limit.
func findClosingReferences(text string, limit int) ([]closingReference, bool) {
	matches := closingKeywordRegex.FindAllStringSubmatchIndex(text, limit+1)
	refs := make([]closingReference, 0, min(len(matches), limit))
	for i := 0; i < len(matches) && i < limit; i++ {
		refs = append(refs, newClosingReference(text, matches[i]))
	}
	return refs, len(matches) > limit
}

// newClosingReference builds the reference of one closingKeywordRegex match m in text.
func newClosingReference(text string, m []int) closingReference {
	group := func(n int) string {
		if m[2*n] < 0 {
			return ""
		}
		return text[m[2*n]:m[2*n+1]]
	}
	ref := closingReference{
		keyword: group(1),
		ref:     group(2),
		owner:   firstNonEmpty(group(3), group(7)),
		repo:    firstNonEmpty(group(4), group(8)),
		pull:    strings.HasPrefix(strings.ToLower(group(5)), "pull"),
		local:   group(9) != "" && group(7) == "" && group(8) == "",
		line:    strings.Count(text[:m[0]], "\n") + 1,
	}
	for _, n := range []int{6, 9, 10} {
		if num, err := strconv.Atoi(group(n)); err == nil {
			ref.number = num
		}
	}
	return ref
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// allowedClosingReferences returns the references the closingAllowMarker lines among lines mark
// as intended, lower-cased.
func allowedClosingReferences(lines []string) map[string]bool {
	allowed := make(map[string]bool)
	for i := 0; i < len(lines) && i < maxCommitMessageLines; i++ {
		rest, ok := cutPrefixFold(strings.TrimSpace(lines[i]), closingAllowMarker)
		if ok && rest != "" && !strings.ContainsAny(rest, " \t") {
			allowed[strings.ToLower(rest)] = true
		}
	}
	return allowed
}

// cutPrefixFold is strings.CutPrefix with prefix compared without case; the rest is trimmed.
func cutPrefixFold(s, prefix string) (string, bool) {
	if len(s) < len(prefix) || !strings.EqualFold(s[:len(prefix)], prefix) {
		return "", false
	}
	return strings.TrimSpace(s[len(prefix):]), true
}

// ClosingReferenceTarget is the forge object a closing keyword names.
type ClosingReferenceTarget struct {
	Ref    string
	Owner  string
	Repo   string
	Number int
}

// ClosingReferenceResolution is the forge answer for one closing reference.
type ClosingReferenceResolution struct {
	Kind   string
	Author string
}

const (
	ClosingTargetIssue       = "issue"
	ClosingTargetPullRequest = "pull_request"
)

// ClosingReferenceResolver resolves a closing reference to an issue or pull request.
type ClosingReferenceResolver interface {
	ResolveClosingReference(context.Context, ClosingReferenceTarget) (ClosingReferenceResolution, error)
}

// ClosingReferencePolicy selects offline warning mode or forge-backed resolution mode.
type ClosingReferencePolicy struct {
	Resolver          ClosingReferenceResolver
	PullRequestAuthor string
}

// closingReferenceErrors is the legacy offline caller: explicit pull request URLs are errors;
// ambiguous references are warnings and therefore omitted here.
func closingReferenceErrors(text, where string) []string {
	errs, _ := closingReferenceFindings(context.Background(), text, where, ClosingReferencePolicy{})
	return errs
}

func closingReferenceFindings(ctx context.Context, text, where string, policy ClosingReferencePolicy) ([]string, []string) {
	refs, over := findClosingReferences(text, maxClosingReferences)
	if over {
		return []string{fmt.Sprintf("the %s holds more than %d closing references (close, fix or resolve next to an issue or "+
			"pull request reference); it is refused rather than checked in part", where, maxClosingReferences)}, nil
	}
	allowed := allowedClosingReferences(strings.Split(text, "\n"))
	var errs []string
	var warnings []string
	for i := 0; i < len(refs); i++ {
		if allowed[strings.ToLower(refs[i].ref)] {
			continue
		}
		if policy.Resolver == nil {
			err, warning := offlineClosingReferenceFinding(refs[i], where)
			if err != "" {
				errs = append(errs, err)
			}
			if warning != "" {
				warnings = append(warnings, warning)
			}
			continue
		}
		if err := resolvedClosingReferenceError(ctx, refs[i], where, policy); err != "" {
			errs = append(errs, err)
		}
	}
	return errs, warnings
}

func offlineClosingReferenceFinding(ref closingReference, where string) (string, string) {
	base := fmt.Sprintf("%s line %d: closing keyword %q next to %q", where, ref.line, ref.keyword, ref.ref)
	if ref.pull {
		return fmt.Sprintf("%s names a pull request URL; offline hooks refuse pull request closures unless the line \"%s %s\" marks it as intended",
			base, closingAllowMarker, ref.ref), ""
	}
	return "", fmt.Sprintf("%s will be resolved by `praetorctl forge check-commits` in CI; the offline hook cannot tell an issue from a pull request",
		base)
}

func resolvedClosingReferenceError(ctx context.Context, ref closingReference, where string, policy ClosingReferencePolicy) string {
	resolved, err := policy.Resolver.ResolveClosingReference(ctx, closingReferenceTarget(ref))
	if err != nil {
		return fmt.Sprintf("%s line %d: closing reference %q could not be resolved: %v", where, ref.line, ref.ref, err)
	}
	switch resolved.Kind {
	case ClosingTargetIssue:
		return ""
	case ClosingTargetPullRequest:
		return pullRequestClosingError(ref, where, resolved.Author, policy.PullRequestAuthor)
	default:
		return fmt.Sprintf("%s line %d: closing reference %q resolved to unknown target kind %q", where, ref.line, ref.ref, resolved.Kind)
	}
}

func pullRequestClosingError(ref closingReference, where, author, currentAuthor string) string {
	if author != "" && currentAuthor != "" && strings.EqualFold(author, currentAuthor) {
		return ""
	}
	reason := fmt.Sprintf("pull request author %q differs from checked pull request author %q", author, currentAuthor)
	if currentAuthor == "" {
		reason = fmt.Sprintf("pull request author %q cannot be compared because the checked pull request author is unknown", author)
	}
	return fmt.Sprintf("%s line %d: closing keyword %q next to %q would close a pull request; %s. Add \"%s %s\" only when this closure is intentional",
		where, ref.line, ref.keyword, ref.ref, reason, closingAllowMarker, ref.ref)
}

func closingReferenceTarget(ref closingReference) ClosingReferenceTarget {
	return ClosingReferenceTarget{Ref: ref.ref, Owner: ref.owner, Repo: ref.repo, Number: ref.number}
}

// ResolveClosingReference resolves one closing reference through GitHub's issues endpoint. GitHub
// exposes pull requests through that endpoint with a "pull_request" member, so this distinguishes
// ordinary issues from pull requests without listing either collection.
func (g *GitHubDriver) ResolveClosingReference(ctx context.Context, target ClosingReferenceTarget) (ClosingReferenceResolution, error) {
	owner, repo, err := g.referenceRepository(target)
	if err != nil {
		return ClosingReferenceResolution{}, err
	}
	path := fmt.Sprintf("/repos/%s/%s/issues/%d", owner, repo, target.Number)
	body, status, err := g.sendRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return ClosingReferenceResolution{}, err
	}
	if status == http.StatusNotFound {
		return ClosingReferenceResolution{}, fmt.Errorf("%s not found", target.Ref)
	}
	if status != http.StatusOK {
		return ClosingReferenceResolution{}, fmt.Errorf("github returned HTTP %d for %s: %s", status, target.Ref, util.BodyPreview(body))
	}
	return parseClosingReferenceResolution(target.Ref, body)
}

func (g *GitHubDriver) referenceRepository(target ClosingReferenceTarget) (string, string, error) {
	if target.Number <= 0 {
		return "", "", fmt.Errorf("%s carries no numeric issue or pull request id", target.Ref)
	}
	owner, repo := target.Owner, target.Repo
	if owner == "" || repo == "" {
		return g.resolveRepository()
	}
	if err := util.ValidateGitHubRepositoryIdentity(owner, repo); err != nil {
		return "", "", fmt.Errorf("closing reference %s: %w", target.Ref, err)
	}
	return owner, repo, nil
}

func parseClosingReferenceResolution(ref string, data []byte) (ClosingReferenceResolution, error) {
	var raw struct {
		User struct {
			Login string `json:"login"`
		} `json:"user"`
		PullRequest json.RawMessage `json:"pull_request"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return ClosingReferenceResolution{}, fmt.Errorf("parse %s: %w", ref, err)
	}
	if len(raw.PullRequest) == 0 {
		return ClosingReferenceResolution{Kind: ClosingTargetIssue, Author: raw.User.Login}, nil
	}
	return ClosingReferenceResolution{Kind: ClosingTargetPullRequest, Author: raw.User.Login}, nil
}
