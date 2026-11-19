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
	repoRaw  string
	argument string
	explicit bool
	unknown  string
}

// evaluatePRTitleLimits checks explicit titles before checkout governance can skip a command.
func evaluatePRTitleLimits(ctx context.Context, canonical Canonical, in Invocation) Verdict {
	return evaluatePRTitleLimitsWith(ctx, canonical, in, util.RunGit)
}

// prTitleGitReader shares the bounded git runner and permits deterministic failure fixtures.
type prTitleGitReader func(context.Context, string, ...string) (string, error)

// evaluatePRTitleLimitsWith keeps the no-policy fast path ahead of repository inspection.
func evaluatePRTitleLimitsWith(ctx context.Context, canonical Canonical, in Invocation, runGit prTitleGitReader) Verdict {
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
	var notices []string
	for i := 0; i < len(candidates) && i < maxForgeTextSegments; i++ {
		candidate := candidates[i]
		verdict := judgePRTitle(ctx, candidate, settings.PRTitleLimits, in.Getenv, runGit)
		if verdict.Outcome != Allow {
			return verdict
		}
		notices = appendUniqueNotice(notices, verdict.Notice)
	}
	return Verdict{Outcome: Allow, Notice: strings.Join(notices, "; ")}
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
		if value, raw, next, matched := ghPRDecodedFlag(args, i, "-R", "--repo"); matched {
			candidate.repo, candidate.repoRaw, i = value, raw, next
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

// judgePRTitle applies the target's cap, or the smallest configured cap on uncertainty.
func judgePRTitle(ctx context.Context, candidate ghPRTitleCall, limits []config.PRTitleLimit, getenv func(string) string, runGit prTitleGitReader) Verdict {
	if len(limits) == 0 {
		return Verdict{Outcome: Allow}
	}
	target, err := resolvePRTitleCandidate(ctx, candidate, getenv, runGit)
	limit, capped := prTitleRepositoryLimit(target, limits, err != nil)
	if !capped {
		return Verdict{Outcome: Allow}
	}
	label := "repository " + target
	if err != nil {
		label = "uncertain target repository"
	}
	verdict := judgePRTitleMaximum(candidate, label, limit)
	if err != nil {
		notice := "target repository uncertain: " + err.Error() + "; name the repository with the repo flag"
		if verdict.Outcome == Deny {
			verdict.Reason += "\n" + notice
		} else {
			verdict.Notice = notice
		}
	}
	return verdict
}

// resolvePRTitleCandidate validates raw flag syntax before using the shared decoded value.
func resolvePRTitleCandidate(ctx context.Context, candidate ghPRTitleCall, getenv func(string) string, runGit prTitleGitReader) (string, error) {
	if candidate.repoRaw != "" && (candidate.repo == "" || unresolvedPRTitleArgument(candidate.repoRaw)) {
		return "", fmt.Errorf("repository flag is empty or contains unresolved shell syntax")
	}
	return resolvePRTitleRepository(ctx, candidate.call, candidate.repo, getenv, runGit)
}

// prTitleRepositoryLimit selects exactly one known cap or the minimum across all caps.
func prTitleRepositoryLimit(target string, limits []config.PRTitleLimit, uncertain bool) (int, bool) {
	maximum := limits[0].Max
	for i := 0; i < len(limits) && i < config.MaxPRTitleLimits; i++ {
		if !uncertain && strings.EqualFold(target, limits[i].Repo) {
			return limits[i].Max, true
		}
		maximum = min(maximum, limits[i].Max)
	}
	return maximum, uncertain
}

// judgePRTitleMaximum preserves refusal details and refuses unresolved title syntax.
func judgePRTitleMaximum(candidate ghPRTitleCall, label string, maximum int) Verdict {
	if candidate.unknown != "" {
		return prTitleLimitDeny(fmt.Sprintf("%s: unknown shorthand %q in gh pr create option cluster", label, candidate.unknown))
	}
	if unresolvedPRTitleArgument(candidate.argument) {
		return prTitleLimitDeny(fmt.Sprintf("%s: title argument %q contains unresolved shell syntax", label, candidate.argument))
	}
	length := utf8.RuneCountInString(candidate.title)
	if length > maximum {
		return prTitleLimitDeny(fmt.Sprintf("%s: title length %d exceeds limit %d Unicode code points: %q", label, length, maximum, candidate.title))
	}
	return Verdict{Outcome: Allow}
}

// resolvePRTitleRepository accepts only literal direct targets or a single unchanged remote.
// Wrappers always make the target uncertain, even when they contain an explicit target.
func resolvePRTitleRepository(ctx context.Context, call ghWriteCall, repo string, getenv func(string) string, runGit prTitleGitReader) (string, error) {
	if call.wrapped {
		return "", fmt.Errorf("wrapper before gh command")
	}
	if repo != "" {
		return literalPRTitleRepository(repo)
	}
	if call.repoEnv != nil {
		if unresolvedPRTitleArgument(*call.repoEnv) {
			return "", fmt.Errorf("repository argument contains unresolved shell syntax")
		}
		return literalPRTitleRepository(unquoteShellWord(*call.repoEnv))
	}
	if getenv != nil && getenv("GH_REPO") != "" {
		return "", fmt.Errorf("inherited GH_REPO may be changed by a wrapper")
	}
	if call.prefixUnknown {
		return "", fmt.Errorf("command-prefix environment changes checkout selection")
	}
	host := "github.com"
	if getenv != nil && getenv("GH_HOST") != "" {
		host = getenv("GH_HOST")
	}
	ctx, cancel := context.WithTimeout(ctx, forgeTextReadTimeout)
	defer cancel()
	return ghPRRemoteRepository(ctx, call.dir, host, runGit)
}

// literalPRTitleRepository rejects expansions and empty assignments before normalization.
func literalPRTitleRepository(repo string) (string, error) {
	if repo == "" || unresolvedPRTitleArgument(repo) {
		return "", fmt.Errorf("repository argument is empty or contains unresolved shell syntax")
	}
	return normalizePRTitleRepository(repo)
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

// ghPRRemoteRepository accepts one configured remote only when git reports the same fetch URL.
// The effective URL is read once through the existing bounded runner, under the caller deadline.
func ghPRRemoteRepository(ctx context.Context, dir, host string, runGit prTitleGitReader) (string, error) {
	text, err := runGit(ctx, dir, "config", "--null", "--get-regexp", `^remote\.`)
	if err != nil {
		return "", fmt.Errorf("read repository remote configuration: %w", err)
	}
	remote, err := singlePRTitleRemote(text)
	if err != nil {
		return "", err
	}
	effective, err := runGit(ctx, dir, "remote", "get-url", "--all", remote.name)
	if err != nil {
		return "", fmt.Errorf("read effective fetch URL: %w", err)
	}
	if effective != remote.url {
		return "", fmt.Errorf("effective fetch URL differs from configured URL (rewrite or multiple URLs)")
	}
	if remote.marker != "" && remote.marker != "base" {
		return "", fmt.Errorf("default repository marker selects another target")
	}
	return plainPRTitleRemote(effective, host)
}

// plainPRTitleRemote requires a plain URL on the selected host with exactly two path segments.
func plainPRTitleRemote(effective, host string) (string, error) {
	parsed, err := util.ParseGitRemote(effective)
	if err != nil || strings.ContainsAny(effective, "?#%\n\r\t") {
		return "", fmt.Errorf("effective fetch URL does not parse as a plain repository URL")
	}
	if !strings.EqualFold(parsed.Host, host) {
		return "", fmt.Errorf("remote host differs from configured forge host")
	}
	owner, name, err := util.SplitGitHubRepository(parsed.Path)
	if err != nil {
		return "", fmt.Errorf("effective fetch URL must name one plain owner/repository: %w", err)
	}
	return owner + "/" + name, nil
}

// prTitleRemote retains only the fields needed to validate a single remote, without ranking.
type prTitleRemote struct{ name, url, marker string }

// singlePRTitleRemote reads git's NUL-delimited key/newline/value records without guessing order.
func singlePRTitleRemote(text string) (prTitleRemote, error) {
	var remote prTitleRemote
	records := strings.Split(strings.TrimSuffix(text, "\x00"), "\x00")
	if len(records) > maxForgeTextWords {
		return remote, fmt.Errorf("remote configuration exceeds %d records", maxForgeTextWords)
	}
	for i := 0; i < len(records) && i < maxForgeTextWords; i++ {
		key, value, ok := strings.Cut(records[i], "\n")
		at := strings.LastIndexByte(key, '.')
		if !ok || at <= len("remote.") {
			return remote, fmt.Errorf("remote configuration does not parse")
		}
		if err := acceptPRTitleRemote(&remote, key[len("remote."):at], key[at+1:], value); err != nil {
			return remote, err
		}
	}
	return remote, nil
}

// acceptPRTitleRemote refuses a second remote or fetch URL without choosing between them.
func acceptPRTitleRemote(remote *prTitleRemote, name, field, value string) error {
	if remote.name != "" && remote.name != name {
		return fmt.Errorf("multiple remotes; default markers and remote ranking are uncertain")
	}
	remote.name = name
	switch field {
	case "url":
		if remote.url != "" {
			return fmt.Errorf("multiple configured fetch URLs")
		}
		remote.url = value
	case "gh-resolved":
		remote.marker = value
	}
	return nil
}

// ghCommandRepository retains plain assignment spelling and flags other environment prefixes.
// Wrapper semantics belong to the shared command walker; this reader never interprets env options.
func ghCommandRepository(words []string) (*string, bool) {
	var repo *string
	unknown := false
	for i := 0; i < len(words) && i < maxForgeTextWords; i++ {
		name, value, assigned := forge.ShellAssignment(words[i])
		if assigned && name == "GH_REPO" {
			repo = &value
		} else if assigned {
			unknown = true
		}
	}
	return repo, unknown
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
