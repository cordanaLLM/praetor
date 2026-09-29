// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package bump

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/semver"
	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	// maxPinLookups bounds the upstream questions one VerifyActionPins call asks (HISS-02).
	// A pin met after the budget is spent stays unverified.
	maxPinLookups = 300
	// pinLookupTimeout bounds one upstream question (HISS-02).
	pinLookupTimeout = 20 * time.Second
)

var (
	// actionPinEndpoint is the GitHub REST API the audit asks about SHA pins. Tests point it
	// at a stand-in forge; nothing else changes it.
	actionPinEndpoint = util.DefaultGitHubAPIBase
	// errNoPinLookup marks a verification that had no upstream to ask: no GitHub token.
	errNoPinLookup = errors.New("no GitHub token: set GITHUB_TOKEN or GH_TOKEN, or sign in with gh")
	// errPinLookupBudget marks a pin met after maxPinLookups questions.
	errPinLookupBudget = fmt.Errorf("more SHA pins than one audit asks about (%d questions)", maxPinLookups)
)

// ActionPinLookup asks an action's upstream repository about its commits and tags. The
// audit uses GitHubActionPinLookup; the errors follow forge.GitHubDriver's CommitAt.
type ActionPinLookup interface {
	// CommitAt resolves ref, a commit SHA or refs/tags/<name>, in owner/repo to the commit
	// it names. It returns forge.ErrCommitNotFound when there is none, and
	// forge.ErrRepositoryNotVisible when the repository cannot be read.
	CommitAt(ctx context.Context, owner, repo, ref string) (string, error)
	// TagsAt returns the tags of owner/repo that point at commit.
	TagsAt(ctx context.Context, owner, repo, commit string) ([]string, error)
}

// GitHubActionPinLookup returns an ActionPinLookup that asks the GitHub REST API at endpoint
// (util.GitHubAPIBase normalizes it) with token, through forge.GitHubDriver.
func GitHubActionPinLookup(token, endpoint string) ActionPinLookup {
	return githubPinLookup{token: token, endpoint: endpoint}
}

type githubPinLookup struct{ token, endpoint string }

func (l githubPinLookup) driver(owner, repo string) *forge.GitHubDriver {
	driver := forge.NewGitHubDriver(l.token, l.endpoint)
	driver.SetRepository(owner, repo)
	return driver
}

func (l githubPinLookup) CommitAt(ctx context.Context, owner, repo, ref string) (string, error) {
	return l.driver(owner, repo).CommitAt(ctx, ref)
}

func (l githubPinLookup) TagsAt(ctx context.Context, owner, repo, commit string) ([]string, error) {
	return l.driver(owner, repo).TagsAt(ctx, commit)
}

// auditWorkflowActions is the workflow inventory the audit reports: ScanWorkflowActions, then
// VerifyActionPins against github.com with the token util.ResolveAuthTokenContext finds.
// Without a token every SHA pin stays unverified; nothing is asked when no workflow pins an
// action by SHA.
func auditWorkflowActions(ctx context.Context, repoPath string) ([]ActionCandidate, []DeprecationWarning, error) {
	actions, deprecations, err := ScanWorkflowActions(ctx, repoPath)
	if err != nil || !slices.ContainsFunc(actions, func(a ActionCandidate) bool { return a.PinnedSHA != "" }) {
		return actions, deprecations, err
	}
	var lookup ActionPinLookup
	if token := util.ResolveAuthTokenContext(ctx, ""); token != "" {
		lookup = GitHubActionPinLookup(token, actionPinEndpoint)
	}
	verified, findings := VerifyActionPins(ctx, actions, lookup)
	return verified, append(deprecations, findings...), nil
}

// VerifyActionPins asks each SHA-pinned action's upstream repository about the commit it
// pins (#609) and returns the actions with their Pin verdict, plus one failing finding per
// refuted pin:
//
//   - PinVerified: the tag of the release the comment names points at the pinned commit,
//     after peeling an annotated tag, or the commit carries a tag equal to the release at
//     the release's precision (a moving "v4" that has moved on, "7.0.0" for tag v7.0.0).
//     Drift is then judged from that release.
//   - PinReleaseMismatch: otherwise, when the release's tag points at another commit or does
//     not exist. The finding names the tags the pinned commit does carry.
//   - PinCommitMissing: the upstream has no such commit, so the job stops at setup.
//   - PinUnversioned: the pin names no release, and the upstream has the commit.
//   - PinUnverified: the question could not be asked or answered. A nil lookup, a transport
//     error, a rate limit or any other unexpected answer stops all later questions, so an
//     unreachable upstream costs one timeout, not one per pin.
//
// Any verdict other than PinVerified clears UpToDate: a pin nobody confirmed is never
// reported current. Each finding names the workflow file and line. Identical pins are asked
// about once. Actions not pinned by SHA pass through unchanged.
func VerifyActionPins(ctx context.Context, actions []ActionCandidate, lookup ActionPinLookup) ([]ActionCandidate, []DeprecationWarning) {
	verifier := &pinVerifier{lookup: lookup, verdicts: make(map[string]pinVerdict)}
	if lookup == nil {
		verifier.halted = errNoPinLookup
	}
	verified := slices.Clone(actions)
	var findings []DeprecationWarning
	for index := range verified {
		action := &verified[index]
		if action.PinnedSHA == "" {
			continue
		}
		verdict := verifier.verify(ctx, *action)
		action.Pin, action.PinDetail = verdict.status, verdict.detail
		if verdict.status != PinVerified {
			action.UpToDate = false
		}
		if finding, failed := pinFinding(*action); failed {
			findings = append(findings, finding)
		}
	}
	return verified, findings
}

// pinVerdict is the upstream's answer about one pin and its reason.
type pinVerdict struct {
	status PinStatus
	detail string
}

// pinVerifier asks the upstream about pins, remembers each answer and stops asking once an
// answer says the upstream cannot be asked.
type pinVerifier struct {
	lookup   ActionPinLookup
	asked    int
	halted   error
	verdicts map[string]pinVerdict
}

// verify returns the verdict on action's pin, asking the upstream once per distinct pin.
func (v *pinVerifier) verify(ctx context.Context, action ActionCandidate) pinVerdict {
	owner, repo, err := actionRepository(action.Action)
	if err != nil {
		return unverifiedPin(err)
	}
	release := ""
	if action.CurrentVersion != action.PinnedSHA {
		release = action.CurrentVersion
	}
	key := owner + "/" + repo + "@" + action.PinnedSHA + "#" + release
	if verdict, known := v.verdicts[key]; known {
		return verdict
	}
	verdict := v.resolve(ctx, owner, repo, action.PinnedSHA, release)
	v.verdicts[key] = verdict
	return verdict
}

// resolve asks about the release's tag first: when it points at sha, the pin is verified in
// one question. Otherwise it asks whether sha exists, and for a refuted release which tags
// sha carries.
func (v *pinVerifier) resolve(ctx context.Context, owner, repo, sha, release string) pinVerdict {
	tagged := ""
	if release != "" {
		commit, err := askUpstream(ctx, v, func(ctx context.Context) (string, error) {
			return v.lookup.CommitAt(ctx, owner, repo, "refs/tags/"+release)
		})
		switch {
		case err == nil && commit == sha:
			return pinVerdict{status: PinVerified}
		case err != nil && !errors.Is(err, forge.ErrCommitNotFound):
			return unverifiedPin(err)
		}
		tagged = commit
	}
	if _, err := askUpstream(ctx, v, func(ctx context.Context) (string, error) {
		return v.lookup.CommitAt(ctx, owner, repo, sha)
	}); err != nil {
		verdict := missingOrUnverified(err, owner, repo, sha)
		if verdict.status == PinCommitMissing && tagged != "" {
			verdict.detail += fmt.Sprintf("; tag %s points at %s", release, tagged)
		}
		return verdict
	}
	if release == "" {
		return pinVerdict{status: PinUnversioned, detail: "the upstream has the commit, but no release comment names it"}
	}
	return v.mismatch(ctx, owner, repo, sha, release, tagged)
}

// mismatch decides a release comment whose tag does not name sha, from the tags sha carries.
// A carried tag equal to the release at the release's own precision confirms the comment
// (semver.ParseTag): "# v4" on a v4.1.0 commit after v4 moved on, or "# 7.0.0" on the commit
// tagged v7.0.0. Otherwise the comment is refuted, naming where its tag points, or that it
// has none, and the tags sha carries. A listing that fails decides nothing.
func (v *pinVerifier) mismatch(ctx context.Context, owner, repo, sha, release, tagged string) pinVerdict {
	carried, err := askUpstream(ctx, v, func(ctx context.Context) ([]string, error) {
		return v.lookup.TagsAt(ctx, owner, repo, sha)
	})
	if err != nil {
		return unverifiedPin(fmt.Errorf("list the tags on the pinned commit: %w", err))
	}
	if tag, inLine := releaseLineTag(release, carried); inLine {
		return pinVerdict{status: PinVerified, detail: fmt.Sprintf("the pinned commit carries %s, release %s at its precision", tag, release)}
	}
	claim := fmt.Sprintf("release comment %s: %s/%s has no tag %s", release, owner, repo, release)
	if tagged != "" {
		claim = fmt.Sprintf("release comment %s: tag %s points at %s, not at the pinned commit", release, release, tagged)
	}
	if len(carried) == 0 {
		return pinVerdict{status: PinReleaseMismatch, detail: claim + "; no tag points at the pinned commit"}
	}
	return pinVerdict{status: PinReleaseMismatch, detail: claim + "; the pinned commit carries " + strings.Join(carried, ", ")}
}

// releaseLineTag returns the first of tags that names release at release's precision, and
// whether there is one.
func releaseLineTag(release string, tags []string) (string, bool) {
	want, precision, ok := semver.ParseTag(release)
	if !ok {
		return "", false
	}
	for _, tag := range tags {
		got, _, isTag := semver.ParseTag(tag)
		if isTag && semver.Compare(got.Truncate(precision), want.Truncate(precision)) == 0 {
			return tag, true
		}
	}
	return "", false
}

// askUpstream asks one question under pinLookupTimeout, unless the verifier has halted or
// spent maxPinLookups. An answer that is neither a result nor a definite "not found" halts
// the verifier: every later pin is unverified for the same reason.
func askUpstream[T any](ctx context.Context, v *pinVerifier, question func(context.Context) (T, error)) (T, error) {
	var zero T
	if v.halted != nil {
		return zero, v.halted
	}
	if v.asked >= maxPinLookups {
		return zero, errPinLookupBudget
	}
	v.asked++
	askCtx, cancel := context.WithTimeout(ctx, pinLookupTimeout)
	defer cancel()
	answer, err := question(askCtx)
	if err != nil && !errors.Is(err, forge.ErrCommitNotFound) && !errors.Is(err, forge.ErrRepositoryNotVisible) {
		v.halted = err
	}
	return answer, err
}

// missingOrUnverified reads the answer to "does sha exist": a definite no is PinCommitMissing,
// anything else PinUnverified.
func missingOrUnverified(err error, owner, repo, sha string) pinVerdict {
	if errors.Is(err, forge.ErrCommitNotFound) {
		return pinVerdict{status: PinCommitMissing, detail: fmt.Sprintf("commit %s does not exist in %s/%s; the job stops at setup", sha, owner, repo)}
	}
	return unverifiedPin(err)
}

// unverifiedPin is the verdict on a pin the upstream did not answer about.
func unverifiedPin(err error) pinVerdict {
	return pinVerdict{status: PinUnverified, detail: err.Error()}
}

// actionRepository returns the owner and repository of an owner/repository[/path] action.
func actionRepository(action string) (string, string, error) {
	parts := strings.SplitN(action, "/", 3)
	if len(parts) < 2 {
		return "", "", fmt.Errorf("action %q names no owner/repository", action)
	}
	if err := util.ValidateGitHubRepositoryIdentity(parts[0], parts[1]); err != nil {
		return "", "", fmt.Errorf("action %q: %w", action, err)
	}
	return parts[0], parts[1], nil
}

// pinFinding is the failing finding for a pin its upstream refuted, naming file and line.
func pinFinding(action ActionCandidate) (DeprecationWarning, bool) {
	var kind string
	switch action.Pin {
	case PinCommitMissing:
		kind = "action-pin-commit-missing"
	case PinReleaseMismatch:
		kind = "action-pin-release-mismatch"
	default:
		return DeprecationWarning{}, false
	}
	return DeprecationWarning{
		Component: action.Action + "@" + action.PinnedSHA,
		Kind:      kind,
		Details:   action.PinDetail + " in " + workflowLocation(action),
	}, true
}
