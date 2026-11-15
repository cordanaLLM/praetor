// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package agenthook

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/hiss"
	"github.com/cordanaLLM/praetor/internal/util"
)

// ghPRTitleCall pairs an explicit title and target with the shared parser's call.
type ghPRTitleCall struct {
	call     ghWriteCall
	title    string
	repo     string
	argument string
	explicit bool
	unknown  string
}

// evaluatePRTitleLimits checks explicit titles before checkout governance can skip a command.
func evaluatePRTitleLimits(ctx context.Context, canonical Canonical, in Invocation) Verdict {
	if canonical.Command == "" {
		return Verdict{Outcome: Allow}
	}
	ctx, cancel := context.WithTimeout(ctx, forgeTextReadTimeout)
	defer cancel()
	settings, settingsErr := loadOperatorForgeSettings(ctx, in)
	if settingsErr == nil && len(settings.PRTitleLimits) == 0 {
		return Verdict{Outcome: Allow}
	}
	calls, err := collectGHWriteCalls(canonical.Command, commandBase(canonical, in.WorkDir))
	if err != nil {
		return forgeTextDeny("shell reader: "+err.Error(), false)
	}
	candidates := explicitPRTitleCalls(calls)
	if len(candidates) == 0 {
		return Verdict{Outcome: Allow}
	}
	if settingsErr != nil {
		return prTitleLimitDeny("operator forge settings unavailable: " + settingsErr.Error())
	}
	for i := 0; i < len(candidates) && i < maxForgeTextSegments; i++ {
		candidate := candidates[i]
		verdict := judgePRTitle(ctx, candidate, settings.PRTitleLimits, in.Getenv)
		if verdict.Outcome != Allow {
			return verdict
		}
	}
	return Verdict{Outcome: Allow}
}

// explicitPRTitleCalls selects create calls with an explicit title or unknown shorthand.
func explicitPRTitleCalls(calls []ghWriteCall) []ghPRTitleCall {
	var candidates []ghPRTitleCall
	for i := 0; i < len(calls) && i < maxForgeTextSegments; i++ {
		if !calls[i].prCreate {
			continue
		}
		candidate := ghPRTitleOptions(calls[i].args)
		if candidate.explicit || candidate.unknown != "" {
			candidate.call = calls[i]
			candidates = append(candidates, candidate)
		}
	}
	return candidates
}

// ghPRTitleOptions reads title and repository flags through the shared gh flag reader.
func ghPRTitleOptions(args []string) ghPRTitleCall {
	var candidate ghPRTitleCall
	for i := 0; i < len(args) && i < maxForgeTextWords; i++ {
		if unquoteShellWord(args[i]) == "--" {
			break
		}
		if value, raw, next, matched := ghPRDecodedFlag(args, i, "-t", "--title"); matched {
			candidate.title, candidate.argument, candidate.explicit, i = value, raw, true, next
			continue
		}
		if value, _, next, matched := ghPRDecodedFlag(args, i, "-R", "--repo"); matched {
			candidate.repo, i = value, next
			continue
		}
		word := unquoteShellWord(args[i])
		if _, _, unknown := ghCreateShortOption(word); unknown != "" {
			candidate.unknown = unknown
		}
		if ghPRValueFlag(word) {
			i++
		}
	}
	return candidate
}

// ghPRDecodedFlag decodes the whole shell argument once and preserves its raw spelling.
func ghPRDecodedFlag(args []string, i int, short, long string) (string, string, int, bool) {
	value, next, matched, _ := flagValue(args, i, short, short, long)
	if next != i {
		value = unquoteShellWord(value)
	}
	return value, args[next], next, matched
}

// ghPRValueFlag identifies separate values, including after boolean clusters, that remain data.
func ghPRValueFlag(word string) bool {
	if option, tail, _ := ghCreateShortOption(word); option != "" {
		if tail != "" {
			return false
		}
		word = option
	}
	switch word {
	case "-a", "--assignee", "--attach", "-B", "--base", "-b", "--body", "-F", "--body-file",
		"-H", "--head", "-l", "--label", "-m", "--milestone", "-p", "--project",
		"--recover", "-r", "--reviewer", "-T", "--template", "--hostname":
		return true
	default:
		return false
	}
}

// judgePRTitle resolves the target and counts Unicode code points against its configured cap.
func judgePRTitle(ctx context.Context, candidate ghPRTitleCall, limits []config.PRTitleLimit, getenv func(string) string) Verdict {
	target, err := resolvePRTitleRepository(ctx, candidate.call, candidate.repo, getenv)
	if err != nil {
		return prTitleLimitDeny("target repository unavailable: " + err.Error())
	}
	length := utf8.RuneCountInString(candidate.title)
	for i := 0; i < len(limits) && i < config.MaxPRTitleLimits; i++ {
		limit := limits[i]
		if !strings.EqualFold(target, limit.Repo) {
			continue
		}
		if candidate.unknown != "" {
			return prTitleLimitDeny(fmt.Sprintf("repository %s: unknown shorthand %q in gh pr create option cluster", target, candidate.unknown))
		}
		if unresolvedPRTitleArgument(candidate.argument) {
			return prTitleLimitDeny(fmt.Sprintf("repository %s: title argument %q contains unresolved shell syntax", target, candidate.argument))
		}
		if length > limit.Max {
			return prTitleLimitDeny(fmt.Sprintf("repository %s: title length %d exceeds limit %d Unicode code points: %q", target, length, limit.Max, candidate.title))
		}
	}
	return Verdict{Outcome: Allow}
}

// resolvePRTitleRepository prefers explicit flags, local and inherited GH_REPO, then git defaults.
func resolvePRTitleRepository(ctx context.Context, call ghWriteCall, repo string, getenv func(string) string) (string, error) {
	if repo == "" {
		repo = ghPRRepositoryEnvironment(call.repoEnv, getenv)
	}
	if repo == "" {
		var err error
		repo, err = ghPRDefaultRepository(ctx, call.dir)
		if err != nil {
			return "", err
		}
	}
	if repo != "" {
		return normalizePRTitleRepository(repo)
	}
	return ghPRRemoteRepository(ctx, call.dir)
}

// ghPRRepositoryEnvironment preserves an empty command-local override of inherited GH_REPO.
func ghPRRepositoryEnvironment(local *string, getenv func(string) string) string {
	if local != nil {
		return *local
	}
	if getenv != nil {
		return getenv("GH_REPO")
	}
	return ""
}

// normalizePRTitleRepository accepts gh's URL and host-prefixed forms as owner/repo policy keys.
func normalizePRTitleRepository(repo string) (string, error) {
	if strings.Contains(repo, ":") {
		remote, err := util.ParseGitRemote(repo)
		if err != nil {
			return "", err
		}
		repo = remote.Path
	} else {
		repo = util.CleanGitURL(repo)
		parts := strings.Split(repo, "/")
		if len(parts) == 3 {
			repo = strings.Join(parts[1:], "/")
		}
	}
	owner, name, err := util.SplitGitHubRepository(repo)
	if err != nil {
		return "", err
	}
	return owner + "/" + name, nil
}

// ghPRRemoteRepository follows cli/cli v2.104.0 context/remote.go's remoteNameSortScore.
// https://github.com/cli/cli/blob/v2.104.0/context/remote.go
// Invalid network remotes are ignored as TranslateRemotes does; unanswered git reads fail closed.
func ghPRRemoteRepository(ctx context.Context, dir string) (string, error) {
	text, err := util.RunGit(ctx, dir, "remote")
	if err != nil {
		return "", fmt.Errorf("list gh repository remotes: %w", err)
	}
	names := strings.Fields(text)
	if len(names) > maxForgeTextWords {
		return "", fmt.Errorf("gh repository exceeds %d remotes", maxForgeTextWords)
	}
	selected, score := "", -1
	for i := 0; i < len(names) && i < maxForgeTextWords; i++ {
		remote, err := util.ReadGitRemote(ctx, dir, names[i])
		if errors.Is(err, util.ErrGitRemoteNotNetwork) {
			continue
		}
		if err != nil {
			return "", err
		}
		if rank := ghPRRemoteRank(names[i]); rank > score {
			selected, score = remote.Owner+"/"+remote.Repo, rank
		}
	}
	if selected == "" {
		return "", fmt.Errorf("%w: no gh repository remote", util.ErrRepoIdentityUnresolved)
	}
	return selected, nil
}

// ghPRRemoteRank matches gh's case-insensitive upstream, github, origin, then other ordering.
func ghPRRemoteRank(name string) int {
	switch strings.ToLower(name) {
	case "upstream":
		return 3
	case "github":
		return 2
	case "origin":
		return 1
	default:
		return 0
	}
}

// ghPRDefaultRepository reads gh repo set-default's remote resolution from checkout git config.
func ghPRDefaultRepository(ctx context.Context, dir string) (string, error) {
	text, err := util.RunGit(ctx, dir, "config", "--local", "--get-regexp", `^remote\..*\.gh-resolved$`)
	if err != nil && !util.GitAnsweredUnset(ctx, err) {
		return "", fmt.Errorf("read gh default repository: %w", err)
	}
	lines := strings.Split(strings.TrimSpace(text), "\n")
	for i := 0; i < len(lines) && i < maxForgeTextWords; i++ {
		key, value, ok := strings.Cut(lines[i], " ")
		if !ok || value == "" {
			continue
		}
		if value != "base" {
			return value, nil
		}
		name := strings.TrimSuffix(strings.TrimPrefix(key, "remote."), ".gh-resolved")
		remote, err := util.ReadGitRemote(ctx, dir, name)
		if err != nil {
			return "", err
		}
		return remote.Owner + "/" + remote.Repo, nil
	}
	return "", nil
}

// ghCommandRepository captures GH_REPO assignments and env removals in the command prefix.
func ghCommandRepository(words []string) *string {
	var repo *string
	for i := 0; i < len(words) && i < maxForgeTextWords; i++ {
		name, value, assigned := forge.ShellAssignment(words[i])
		if assigned && name == "GH_REPO" {
			decoded := unquoteShellWord(value)
			repo = &decoded
		} else if strings.HasPrefix(unquoteShellWord(words[i]), "-") {
			option, value, next := directoryOption(words, i)
			i = next
			if (option == "-u" || option == "--unset") && unquoteShellWord(value) == "GH_REPO" {
				empty := ""
				repo = &empty
			}
		}
	}
	return repo
}

// unresolvedPRTitleArgument rejects expansions while preserving quoted and escaped literal data.
func unresolvedPRTitleArgument(raw string) bool {
	var quote hiss.QuoteStepper
	for i := 0; i < len(raw); i++ {
		q := quote.OpenQuote()
		if q != '\'' && strings.ContainsRune("$`", rune(raw[i])) {
			return true
		}
		if q == 0 && strings.ContainsRune("*?[]{}~", rune(raw[i])) {
			return true
		}
		i = quote.Step(raw, i)
	}
	return false
}

// prTitleLimitDeny names the operator rule in a client-independent refusal.
func prTitleLimitDeny(detail string) Verdict {
	return Verdict{Outcome: Deny, Reason: refusal("HISS", "pr-title-limit: "+detail)}
}
