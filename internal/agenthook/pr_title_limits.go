// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package agenthook

import (
	"context"
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

// explicitPRTitleCalls selects only parsed gh pr create calls carrying an explicit title.
func explicitPRTitleCalls(calls []ghWriteCall) []ghPRTitleCall {
	var candidates []ghPRTitleCall
	for i := 0; i < len(calls) && i < maxForgeTextSegments; i++ {
		if !calls[i].prCreate {
			continue
		}
		title, repo, argument, explicit := ghPRTitleOptions(calls[i].args)
		if explicit {
			candidates = append(candidates, ghPRTitleCall{call: calls[i], title: title, repo: repo, argument: argument})
		}
	}
	return candidates
}

// ghPRTitleOptions reads title and repository flags through the shared gh flag reader.
func ghPRTitleOptions(args []string) (title, repo, argument string, explicit bool) {
	for i := 0; i < len(args) && i < maxForgeTextWords; i++ {
		if unquoteShellWord(args[i]) == "--" {
			break
		}
		if value, raw, next, matched := ghPRDecodedFlag(args, i, "-t", "--title"); matched {
			title, argument, explicit, i = value, raw, true, next
			continue
		}
		if value, _, next, matched := ghPRDecodedFlag(args, i, "-R", "--repo"); matched {
			repo, i = value, next
			continue
		}
		if ghPRValueFlag(unquoteShellWord(args[i])) {
			i++
		}
	}
	return title, repo, argument, explicit
}

// ghPRDecodedFlag decodes the whole shell argument once and preserves its raw spelling.
func ghPRDecodedFlag(args []string, i int, short, long string) (string, string, int, bool) {
	value, next, matched, _ := flagValue(args, i, short, short, long)
	if next != i {
		value = unquoteShellWord(value)
	}
	return value, args[next], next, matched
}

// ghPRValueFlag identifies other separate-value create flags so their values remain data.
func ghPRValueFlag(word string) bool {
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
	owner, name, err := util.ResolveRemoteIdentity(ctx, call.dir)
	if err != nil {
		return "", err
	}
	return owner + "/" + name, nil
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
	repo = strings.TrimPrefix(strings.TrimPrefix(repo, "https://"), "http://")
	parts := strings.Split(repo, "/")
	if len(parts) == 3 {
		repo = strings.Join(parts[1:], "/")
	}
	owner, name, err := util.SplitGitHubRepository(repo)
	if err != nil {
		return "", err
	}
	return owner + "/" + name, nil
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

// ghCommandRepository captures GH_REPO assignments only in the parsed command prefix.
func ghCommandRepository(words []string) *string {
	var repo *string
	for i := 0; i < len(words) && i < maxForgeTextWords; i++ {
		name, value, assigned := forge.ShellAssignment(words[i])
		if assigned && name == "GH_REPO" {
			decoded := unquoteShellWord(value)
			repo = &decoded
		} else if strings.HasPrefix(words[i], "-") {
			_, _, i = directoryOption(words, i)
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
