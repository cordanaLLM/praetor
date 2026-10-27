// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package release

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	// ReleasePREnv is the environment variable that marks a release pull request run.
	ReleasePREnv = "PRAETOR_RELEASE_PR"
	// GitHubHeadRefEnv is the GitHub Actions pull request head branch variable.
	GitHubHeadRefEnv = "GITHUB_HEAD_REF"
	// ReleasePleaseBranchPrefix is the head branch prefix release-please opens release PRs from.
	ReleasePleaseBranchPrefix = "release-please--"
	// E2ETestTrailerKey is the commit trailer key that names a declared end-to-end test id.
	E2ETestTrailerKey = "E2E-Test:"

	maxGitDescribeBytes = 4096
	maxGitLogBytes      = 1 << 20
	maxGitShowBytes     = 1 << 16
	maxGitDiffTreeBytes = 1 << 18
	maxReleaseCommits   = 512
	maxChangedPaths     = 4096
	maxCommitBodyLines  = 1024
	sha12Len            = 12
)

// evidenceMutantAlwaysTrue is toggled in mutation tests to prove that a stubbed
// evidence check fails the planted negative test case (order engine-release-feature-e2e-gate-20261027).
var evidenceMutantAlwaysTrue = false

// E2ESettings holds the resolved settings for the release end-to-end evidence check
// (order engine-release-feature-e2e-gate-20261027).
type E2ESettings struct {
	Paths []string
	Tests []string
	Since string
}

// SettingsFromPolicy builds E2ESettings from a manifest ReleaseE2EPolicy, falling back to
// config.DefaultReleaseE2EPaths when policy or policy.Paths is empty.
func SettingsFromPolicy(policy *config.ReleaseE2EPolicy) E2ESettings {
	if policy == nil {
		return E2ESettings{Paths: config.DefaultReleaseE2EPaths()}
	}
	return E2ESettings{
		Paths: policy.EffectivePaths(),
		Tests: slices.Clone(policy.Tests),
		Since: policy.Since,
	}
}

// EffectivePaths returns s.Paths when non-empty, or config.DefaultReleaseE2EPaths.
func (s E2ESettings) EffectivePaths() []string {
	if len(s.Paths) == 0 {
		return config.DefaultReleaseE2EPaths()
	}
	return slices.Clone(s.Paths)
}

// E2EFinding describes one feat or fix commit since the last release tag that lacks
// end-to-end test evidence or names an undeclared test id.
type E2EFinding struct {
	SHA          string
	Subject      string
	UndeclaredID string
	Reason       string
}

// SHA12 returns the 12-character abbreviated commit SHA.
func (f E2EFinding) SHA12() string {
	if len(f.SHA) >= sha12Len {
		return f.SHA[:sha12Len]
	}
	return f.SHA
}

// E2EResult is the outcome of Judge over a repository.
type E2EResult struct {
	Tag            string
	Skipped        bool
	SkipReason     string
	ReleaseContext bool
	CheckedCommits int
	Findings       []E2EFinding
}

// IsReleaseContext reports whether the current audit runs in release context:
// --release flag, PRAETOR_RELEASE_PR=true, or GITHUB_HEAD_REF starting with release-please--.
func IsReleaseContext(flagRelease bool) bool {
	return ResolveReleaseContext(flagRelease, os.Getenv)
}

// ResolveReleaseContext evaluates release context using getenv.
func ResolveReleaseContext(flagRelease bool, getenv func(string) string) bool {
	if flagRelease {
		return true
	}
	if getenv == nil {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(getenv(ReleasePREnv)), "true") {
		return true
	}
	return strings.HasPrefix(strings.TrimSpace(getenv(GitHubHeadRefEnv)), ReleasePleaseBranchPrefix)
}

// AuditE2E runs the release end-to-end evidence gate and formats its [PASS], [WARN],
// [FAIL], or [SKIP] output lines (order engine-release-feature-e2e-gate-20261027).
func AuditE2E(ctx context.Context, repo string, policy *config.ReleaseE2EPolicy, releaseContext bool) (string, error) {
	result, err := Judge(ctx, repo, SettingsFromPolicy(policy), releaseContext)
	if err != nil {
		return "", fmt.Errorf("[FAIL] Release end-to-end evidence check failed: %w", err)
	}
	return formatE2EResult(result)
}

func formatE2EResult(result E2EResult) (string, error) {
	if result.Skipped {
		return fmt.Sprintf("[SKIP] Release end-to-end evidence not checked: %s.", result.SkipReason), nil
	}
	if len(result.Findings) == 0 {
		return fmt.Sprintf("[PASS] Release end-to-end evidence: %d feat/fix commit(s) since %s verified.",
			result.CheckedCommits, result.Tag), nil
	}
	prefix := "[WARN]"
	if result.ReleaseContext {
		prefix = "[FAIL]"
	}
	lines := make([]string, 0, len(result.Findings))
	for i := 0; i < len(result.Findings) && i < maxReleaseCommits; i++ {
		f := result.Findings[i]
		lines = append(lines, fmt.Sprintf("%s Release end-to-end evidence: %s (%s): %s",
			prefix, f.SHA12(), f.Subject, f.Reason))
	}
	out := strings.Join(lines, "\n")
	if result.ReleaseContext {
		return out, errors.New(out)
	}
	return out, nil
}

// judge returns the end-to-end evidence findings for commits since the last release tag.
func judge(ctx context.Context, repo string, settings E2ESettings, releaseContext bool) ([]E2EFinding, error) {
	res, err := Judge(ctx, repo, settings, releaseContext)
	if err != nil {
		return nil, err
	}
	return res.Findings, nil
}

// Judge inspects feat and fix commits in <last-tag>..HEAD for end-to-end test evidence
// (order engine-release-feature-e2e-gate-20261027).
func Judge(ctx context.Context, repo string, settings E2ESettings, releaseContext bool) (E2EResult, error) {
	if ctx == nil {
		return E2EResult{}, errors.New("release e2e judge: context cannot be nil")
	}
	tag, found, err := lastReleaseTag(ctx, repo)
	if err != nil {
		return E2EResult{}, err
	}
	if !found {
		return E2EResult{
			Skipped:        true,
			SkipReason:     "no release tag matching v[0-9]* is reachable from HEAD",
			ReleaseContext: releaseContext,
		}, nil
	}
	commits, err := listCommitsSinceTag(ctx, repo, tag, settings.Since)
	if err != nil {
		return E2EResult{}, err
	}
	return evaluateReleaseCommits(ctx, repo, tag, commits, settings, releaseContext)
}

type commitSummary struct {
	sha     string
	subject string
}

func evaluateReleaseCommits(
	ctx context.Context,
	repo, tag string,
	commits []commitSummary,
	settings E2ESettings,
	releaseContext bool,
) (E2EResult, error) {
	paths := settings.EffectivePaths()
	declared := make(map[string]bool, len(settings.Tests))
	for i := 0; i < len(settings.Tests) && i < config.MaxReleaseE2ETests; i++ {
		declared[settings.Tests[i]] = true
	}
	res := E2EResult{Tag: tag, ReleaseContext: releaseContext}
	for i := 0; i < len(commits) && i < maxReleaseCommits; i++ {
		c := commits[i]
		if !isFeatOrFixSubject(c.subject) {
			continue
		}
		res.CheckedCommits++
		finding, hasFinding, err := checkCommitEvidence(ctx, repo, c, paths, declared)
		if err != nil {
			return E2EResult{}, err
		}
		if hasFinding {
			res.Findings = append(res.Findings, finding)
		}
	}
	return res, nil
}

func checkCommitEvidence(
	ctx context.Context,
	repo string,
	commit commitSummary,
	paths []string,
	declaredTests map[string]bool,
) (E2EFinding, bool, error) {
	if evidenceMutantAlwaysTrue {
		return E2EFinding{}, false, nil
	}
	trailers, err := commitE2ETrailers(ctx, repo, commit.sha)
	if err != nil {
		return E2EFinding{}, false, err
	}
	for i := 0; i < len(trailers) && i < maxCommitBodyLines; i++ {
		id := trailers[i]
		if !declaredTests[id] {
			return E2EFinding{
				SHA:          commit.sha,
				Subject:      commit.subject,
				UndeclaredID: id,
				Reason:       fmt.Sprintf("commit trailer E2E-Test references undeclared test id %q", id),
			}, true, nil
		}
	}
	if len(trailers) > 0 {
		return E2EFinding{}, false, nil
	}
	matched, err := commitTouchesE2EPath(ctx, repo, commit.sha, paths)
	if err != nil {
		return E2EFinding{}, false, err
	}
	if matched {
		return E2EFinding{}, false, nil
	}
	return E2EFinding{
		SHA:     commit.sha,
		Subject: commit.subject,
		Reason:  "commit neither touches a declared e2e test path nor carries a declared E2E-Test trailer",
	}, true, nil
}

func commitE2ETrailers(ctx context.Context, repo, sha string) ([]string, error) {
	out, ok, err := runGitRead(ctx, repo, maxGitShowBytes, "show", "-s", "--format=%B", sha)
	if err != nil || !ok {
		return nil, fmt.Errorf("read commit body %s: %w", sha, err)
	}
	lines := strings.Split(out, "\n")
	var ids []string
	for i := 0; i < len(lines) && i < maxCommitBodyLines; i++ {
		line := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(line, E2ETestTrailerKey) {
			continue
		}
		id := strings.TrimSpace(strings.TrimPrefix(line, E2ETestTrailerKey))
		ids = append(ids, id)
	}
	return ids, nil
}

func commitTouchesE2EPath(ctx context.Context, repo, sha string, patterns []string) (bool, error) {
	out, ok, err := runGitRead(ctx, repo, maxGitDiffTreeBytes,
		"diff-tree", "--no-commit-id", "--name-only", "-r", "-m", "--root", sha)
	if err != nil || !ok {
		return false, fmt.Errorf("read commit paths %s: %w", sha, err)
	}
	files := strings.Split(out, "\n")
	for i := 0; i < len(files) && i < maxChangedPaths; i++ {
		rel := strings.TrimSpace(files[i])
		if rel == "" {
			continue
		}
		if matchesAnyGlob(rel, patterns) {
			return true, nil
		}
	}
	return false, nil
}

func matchesAnyGlob(rel string, patterns []string) bool {
	relSegs := strings.Split(rel, "/")
	for i := 0; i < len(patterns) && i < config.MaxReleaseE2EPaths; i++ {
		if util.MatchGlobSegments(strings.Split(patterns[i], "/"), relSegs) {
			return true
		}
	}
	return false
}

// isFeatOrFixSubject reports whether subject has Conventional Commits type feat or fix,
// with optional (scope) and optional ! breaking marker followed by ": <description>".
func isFeatOrFixSubject(subject string) bool {
	var rest string
	switch {
	case strings.HasPrefix(subject, "feat"):
		rest = subject[len("feat"):]
	case strings.HasPrefix(subject, "fix"):
		rest = subject[len("fix"):]
	default:
		return false
	}
	if strings.HasPrefix(rest, "(") {
		closeIdx := strings.IndexByte(rest, ')')
		if closeIdx <= 1 || strings.ContainsAny(rest[1:closeIdx], "(\r\n") {
			return false
		}
		rest = rest[closeIdx+1:]
	}
	rest = strings.TrimPrefix(rest, "!")
	if !strings.HasPrefix(rest, ": ") {
		return false
	}
	return strings.TrimSpace(rest[2:]) != ""
}

func lastReleaseTag(ctx context.Context, repo string) (string, bool, error) {
	tag, ok, err := runGitRead(ctx, repo, maxGitDescribeBytes,
		"describe", "--tags", "--abbrev=0", "--match", "v[0-9]*")
	if err != nil || !ok || tag == "" {
		return "", false, err
	}
	return tag, true, nil
}

func listCommitsSinceTag(ctx context.Context, repo, tag, since string) ([]commitSummary, error) {
	args := []string{
		"log",
		"--no-merges",
		fmt.Sprintf("--max-count=%d", maxReleaseCommits),
		"--format=%H%x00%s",
		tag + "..HEAD",
	}
	if since != "" {
		resolved, err := util.ResolveGitCommit(ctx, repo, since)
		if err != nil {
			return nil, fmt.Errorf("resolve release.e2e.since %q: %w", since, err)
		}
		if resolved != "" {
			args = append(args, "^"+resolved)
		}
	}
	out, ok, err := runGitRead(ctx, repo, maxGitLogBytes, args...)
	if err != nil || !ok {
		return nil, fmt.Errorf("list commits %s..HEAD: %w", tag, err)
	}
	return parseCommitLog(out), nil
}

func parseCommitLog(out string) []commitSummary {
	if out == "" {
		return nil
	}
	lines := strings.Split(out, "\n")
	commits := make([]commitSummary, 0, len(lines))
	for i := 0; i < len(lines) && i < maxReleaseCommits; i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		sha, subject, ok := strings.Cut(line, "\x00")
		if !ok || sha == "" {
			continue
		}
		commits = append(commits, commitSummary{sha: sha, subject: subject})
	}
	return commits
}

func runGitRead(ctx context.Context, repoDir string, maxBytes int, args ...string) (string, bool, error) {
	result, err := util.RunGitProbe(ctx, repoDir, maxBytes, args...)
	if err == nil {
		return strings.TrimSpace(string(result.Stdout)), true, nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) ||
		len(result.Stdout) >= maxBytes || len(result.Stderr) >= maxBytes {
		return "", false, fmt.Errorf("git %s in %s: %w", args[0], repoDir, err)
	}
	var exitErr *exec.ExitError
	if errors.Is(err, exec.ErrNotFound) || errors.As(err, &exitErr) {
		return "", false, nil
	}
	return "", false, fmt.Errorf("git %s in %s: %w", args[0], repoDir, err)
}
