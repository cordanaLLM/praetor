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
	"github.com/cordanaLLM/praetor/internal/util"
)

// ghPRTitleCall pairs an explicit title and target with the shared parser's call.
type ghPRTitleCall struct {
	call  ghWriteCall
	title string
	repo  string
}

// evaluatePRTitleLimits checks explicit titles before checkout governance can skip a command.
func evaluatePRTitleLimits(ctx context.Context, canonical Canonical, in Invocation) Verdict {
	if canonical.Command == "" {
		return Verdict{Outcome: Allow}
	}
	ctx, cancel := context.WithTimeout(ctx, forgeTextReadTimeout)
	defer cancel()
	calls, err := collectGHWriteCalls(canonical.Command, commandBase(canonical, in.WorkDir))
	if err != nil {
		return forgeTextDeny("shell reader: "+err.Error(), false)
	}
	candidates := explicitPRTitleCalls(calls)
	if len(candidates) == 0 {
		return Verdict{Outcome: Allow}
	}
	settings, err := loadOperatorForgeSettings(ctx, in.Getenv)
	if err != nil {
		return prTitleLimitDeny("operator forge settings unavailable: " + err.Error())
	}
	if len(settings.PRTitleLimits) == 0 {
		return Verdict{Outcome: Allow}
	}
	for i := 0; i < len(candidates) && i < maxForgeTextSegments; i++ {
		candidate := candidates[i]
		verdict := judgePRTitle(ctx, candidate.call, candidate.title, candidate.repo, settings.PRTitleLimits, in.Getenv)
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
		title, repo, explicit := ghPRTitleOptions(calls[i].args)
		if explicit {
			candidates = append(candidates, ghPRTitleCall{call: calls[i], title: title, repo: repo})
		}
	}
	return candidates
}

// ghPRTitleOptions reads title and repository flags through the shared gh flag reader.
func ghPRTitleOptions(args []string) (title, repo string, explicit bool) {
	for i := 0; i < len(args) && i < maxForgeTextWords; i++ {
		if unquoteShellWord(args[i]) == "--" {
			break
		}
		if value, next, matched, _ := flagValue(args, i, "-t", "--title", "-t"); matched {
			title, explicit, i = unquoteShellWord(value), true, next
			continue
		}
		if value, next, matched, _ := flagValue(args, i, "-R", "--repo", "-R"); matched {
			repo, i = unquoteShellWord(value), next
			continue
		}
		if ghPRValueFlag(unquoteShellWord(args[i])) {
			i++
		}
	}
	return title, repo, explicit
}

// ghPRValueFlag identifies other separate-value create flags so their values remain data.
func ghPRValueFlag(word string) bool {
	switch word {
	case "-a", "--assignee", "--attach", "-B", "--base", "-b", "--body", "-F", "--body-file",
		"-H", "--head", "-l", "--label", "-m", "--milestone", "-p", "--project",
		"--recover", "-r", "--reviewer", "-T", "--template":
		return true
	default:
		return false
	}
}

// judgePRTitle resolves the target and counts Unicode code points against its configured cap.
func judgePRTitle(ctx context.Context, call ghWriteCall, title, repo string, limits []config.PRTitleLimit, getenv func(string) string) Verdict {
	target, err := resolvePRTitleRepository(ctx, call.dir, repo, getenv)
	if err != nil {
		return prTitleLimitDeny("target repository unavailable: " + err.Error())
	}
	length := utf8.RuneCountInString(title)
	for i := 0; i < len(limits) && i < config.MaxPRTitleLimits; i++ {
		limit := limits[i]
		if strings.EqualFold(target, limit.Repo) && length > limit.Max {
			return prTitleLimitDeny(fmt.Sprintf("repository %s: title length %d exceeds limit %d Unicode code points: %q", target, length, limit.Max, title))
		}
	}
	return Verdict{Outcome: Allow}
}

// resolvePRTitleRepository prefers explicit gh flags, GH_REPO, then the command's origin.
func resolvePRTitleRepository(ctx context.Context, dir, repo string, getenv func(string) string) (string, error) {
	if repo == "" && getenv != nil {
		repo = getenv("GH_REPO")
	}
	if repo != "" {
		// gh also accepts HOST/OWNER/REPO; policy keys remain OWNER/REPO.
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
	owner, name, err := util.ResolveRemoteIdentity(ctx, dir)
	if err != nil {
		return "", err
	}
	return owner + "/" + name, nil
}

// prTitleLimitDeny names the operator rule in a client-independent refusal.
func prTitleLimitDeny(detail string) Verdict {
	return Verdict{Outcome: Deny, Reason: refusal("HISS", "pr-title-limit: "+detail)}
}
