// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package needs

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	maxDepguardRules = 256
	maxRuleEntries   = 1024
	maxFilePatterns  = 1024
	maxFilesChecked  = 100000
)

// applyDepguardPolicy checks if any proposed framework replacement is denied by
// depguard rules in .golangci.yml. If so, its status is changed to StatusDeclinedByPolicy,
// and its note names the rule and denied prefix, or "not in the allow list".
func applyDepguardPolicy(ctx context.Context, repoNeeds *RepoNeeds) {
	if ctx == nil || repoNeeds == nil || repoNeeds.Path == "" {
		return
	}
	cfg, err := config.FindGolangciConfig(repoNeeds.Path)
	if err != nil {
		recordDepguardNotice(repoNeeds, repoNeeds.Path, err.Error())
		return
	}
	if cfg == nil {
		return
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
	}
	settings, err := config.LoadDepguardSettings(ctx, cfg.Path)
	if err != nil {
		recordDepguardNotice(repoNeeds, cfg.Path, err.Error())
		return
	}
	if !settings.Enabled {
		return
	}
	if problem := validateRuleModes(settings.Rules); problem != "" {
		recordDepguardNotice(repoNeeds, cfg.Path, problem)
		return
	}
	if len(settings.Rules) == 0 {
		settings.Rules = map[string]config.DepguardRuleConfig{
			"main": {
				ListMode: "original",
				Allow:    []string{"$gostd"},
			},
		}
	}
	applyDepguardToDependencies(repoNeeds, settings)
}

func validateRuleModes(rules map[string]config.DepguardRuleConfig) string {
	names := slices.Sorted(maps.Keys(rules))
	for i := 0; i < len(names) && i < maxDepguardRules; i++ {
		name := names[i]
		mode := strings.ToLower(strings.TrimSpace(rules[name].ListMode))
		if mode != "" && mode != "original" && mode != "strict" && mode != "lax" {
			return fmt.Sprintf("rule %q has unknown list-mode %q", name, rules[name].ListMode)
		}
	}
	return ""
}

func recordDepguardNotice(repoNeeds *RepoNeeds, path, problem string) {
	notice := fmt.Sprintf("%s: %s", path, problem)
	repoNeeds.Deprecations = appendUniqueStr(repoNeeds.Deprecations, notice)
}

func applyDepguardToDependencies(repoNeeds *RepoNeeds, settings *config.DepguardSettings) {
	for i := range repoNeeds.Dependencies {
		dep := &repoNeeds.Dependencies[i]
		if dep.FrameworkReplacement == "" || dep.Status == StatusNative || dep.Relationship != nil {
			continue
		}
		if declined, ruleName, denyEntry := checkDepguard(settings, repoNeeds.Files, dep.FrameworkReplacement); declined {
			dep.Status = StatusDeclinedByPolicy
			if denyEntry != "" {
				dep.Notes = fmt.Sprintf("declined by depguard rule %q: denies %s", ruleName, denyEntry)
			} else {
				dep.Notes = fmt.Sprintf("declined by depguard rule %q: not in the allow list", ruleName)
			}
		}
	}
}

func checkDepguard(settings *config.DepguardSettings, files []string, pkg string) (bool, string, string) {
	ruleNames := slices.Sorted(maps.Keys(settings.Rules))
	for i := 0; i < len(ruleNames) && i < maxDepguardRules; i++ {
		name := ruleNames[i]
		rule := settings.Rules[name]
		if !depguardRuleMatchesFiles(rule, files) {
			continue
		}
		if declined, denyEntry := checkRuleDecision(rule, pkg); declined {
			return true, name, denyEntry
		}
	}
	return false, "", ""
}

func depguardRuleMatchesFiles(rule config.DepguardRuleConfig, files []string) bool {
	if len(rule.Files) == 0 {
		return true
	}
	if len(files) == 0 {
		return ruleHasNoPositivePatterns(rule.Files)
	}
	for i := 0; i < len(files) && i < maxFilesChecked; i++ {
		if fileMatchesRule(rule.Files, files[i]) {
			return true
		}
	}
	return false
}

func ruleHasNoPositivePatterns(patterns []string) bool {
	for i := 0; i < len(patterns) && i < maxFilePatterns; i++ {
		pat := strings.TrimSpace(patterns[i])
		if pat != "" && !strings.HasPrefix(pat, "!") {
			return false
		}
	}
	return true
}

func fileMatchesRule(patterns []string, file string) bool {
	normalized := util.NormalizeSlashes(file)
	hasPositive := false
	positiveMatch := false
	for i := 0; i < len(patterns) && i < maxFilePatterns; i++ {
		pat := strings.TrimSpace(patterns[i])
		if pat == "" {
			continue
		}
		if strings.HasPrefix(pat, "!") {
			neg := strings.TrimSpace(strings.TrimPrefix(pat, "!"))
			if matchFilePattern(neg, normalized) {
				return false
			}
		} else {
			hasPositive = true
			if !positiveMatch && matchFilePattern(pat, normalized) {
				positiveMatch = true
			}
		}
	}
	return !hasPositive || positiveMatch
}

func matchFilePattern(pattern, file string) bool {
	if pattern == "$all" {
		return strings.HasSuffix(file, ".go")
	}
	if pattern == "$test" {
		return strings.HasSuffix(file, "_test.go")
	}
	patSegs := strings.Split(util.NormalizeSlashes(pattern), "/")
	fileSegs := strings.Split(file, "/")
	return util.MatchGlobSegments(patSegs, fileSegs)
}

func checkRuleDecision(rule config.DepguardRuleConfig, pkg string) (bool, string) {
	bestAllow, bestAllowLen := findLongestMatch(rule.Allow, pkg)
	bestDeny, bestDenyLen := findLongestDenyMatch(rule.Deny, pkg)

	mode := strings.ToLower(strings.TrimSpace(rule.ListMode))
	switch mode {
	case "", "original":
		return evalOriginalMode(len(rule.Allow), bestAllow, bestDeny)
	case "strict":
		return evalStrictMode(bestAllow, bestDeny, bestAllowLen, bestDenyLen)
	case "lax":
		return evalLaxMode(bestAllow, bestDeny, bestAllowLen, bestDenyLen)
	default:
		return false, ""
	}
}

func evalOriginalMode(allowCount int, bestAllow, bestDeny string) (bool, string) {
	if bestDeny != "" {
		return true, bestDeny
	}
	if allowCount > 0 && bestAllow == "" {
		return true, ""
	}
	return false, ""
}

func evalStrictMode(bestAllow, bestDeny string, bestAllowLen, bestDenyLen int) (bool, string) {
	if bestAllow == "" {
		return true, bestDeny
	}
	if bestDeny != "" && bestAllowLen <= bestDenyLen {
		return true, bestDeny
	}
	return false, ""
}

func evalLaxMode(bestAllow, bestDeny string, bestAllowLen, bestDenyLen int) (bool, string) {
	if bestDeny == "" {
		return false, ""
	}
	if bestAllow != "" && bestAllowLen > bestDenyLen {
		return false, ""
	}
	return true, bestDeny
}

func findLongestMatch(allow []string, pkg string) (string, int) {
	var best string
	var bestLen int
	for i := 0; i < len(allow) && i < maxRuleEntries; i++ {
		a := allow[i]
		if matched, mlen := matchPackageEntry(a, pkg); matched {
			if best == "" || mlen > bestLen {
				best = a
				bestLen = mlen
			}
		}
	}
	return best, bestLen
}

func findLongestDenyMatch(deny []config.DepguardDenyEntry, pkg string) (string, int) {
	var best string
	var bestLen int
	for i := 0; i < len(deny) && i < maxRuleEntries; i++ {
		d := deny[i]
		if matched, mlen := matchPackageEntry(d.Pkg, pkg); matched {
			if best == "" || mlen > bestLen {
				best = d.Pkg
				bestLen = mlen
			}
		}
	}
	return best, bestLen
}

func matchPackageEntry(entry, importPath string) (bool, int) {
	entry = strings.TrimSpace(entry)
	if entry == "" {
		return false, 0
	}
	// $gostd stands for every standard library package; approximate it by
	// checking that the first path element contains no dot.
	if entry == "$gostd" {
		if isStandardLibrary(importPath) {
			first, _, _ := strings.Cut(importPath, "/")
			return true, len(first)
		}
		return false, 0
	}
	// An entry without any . or / (a standard library path such as os)
	// never matches an import that contains a .
	if !strings.ContainsAny(entry, "./") {
		if strings.ContainsRune(importPath, '.') {
			return false, 0
		}
	}
	if strings.HasSuffix(entry, "$") {
		exact := strings.TrimSuffix(entry, "$")
		if importPath == exact {
			return true, len(entry)
		}
		return false, 0
	}
	if strings.HasPrefix(importPath, entry) {
		return true, len(entry)
	}
	return false, 0
}

func isStandardLibrary(importPath string) bool {
	first, _, _ := strings.Cut(importPath, "/")
	return !strings.ContainsRune(first, '.')
}
