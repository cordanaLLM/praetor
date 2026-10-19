// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package needs

import (
	"context"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/util"
)

// applyDepguardPolicy checks if any proposed framework replacement is denied by
// depguard rules in .golangci.yml. If so, its status is changed to StatusDeclinedByPolicy,
// and its note names the rule and denied prefix.
func applyDepguardPolicy(ctx context.Context, repoNeeds *RepoNeeds) {
	if ctx == nil || repoNeeds == nil || repoNeeds.Path == "" {
		return
	}
	cfg, err := config.FindGolangciConfig(repoNeeds.Path)
	if err != nil || cfg == nil {
		return
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
	}
	settings, err := config.LoadDepguardSettings(ctx, cfg.Path)
	if err != nil || settings == nil || len(settings.Rules) == 0 {
		return
	}
	applyDepguardToDependencies(repoNeeds, settings)
}

func applyDepguardToDependencies(repoNeeds *RepoNeeds, settings *config.DepguardSettings) {
	for i := range repoNeeds.Dependencies {
		dep := &repoNeeds.Dependencies[i]
		if dep.FrameworkReplacement == "" || dep.Status == StatusNative || dep.Relationship != nil {
			continue
		}
		if declined, ruleName, prefix := checkDepguard(settings, repoNeeds.Files, dep.FrameworkReplacement); declined {
			dep.Status = StatusDeclinedByPolicy
			dep.Notes = fmt.Sprintf("declined by depguard rule %q: denies %s", ruleName, prefix)
		}
	}
}

func checkDepguard(settings *config.DepguardSettings, files []string, pkg string) (bool, string, string) {
	ruleNames := slices.Sorted(maps.Keys(settings.Rules))
	for _, name := range ruleNames {
		rule := settings.Rules[name]
		if !depguardRuleMatchesFiles(rule, files) {
			continue
		}
		if declined, prefix := checkDepguardRule(rule, pkg); declined {
			return true, name, prefix
		}
	}
	return false, "", ""
}

func depguardRuleMatchesFiles(rule config.DepguardRuleConfig, files []string) bool {
	if len(rule.Files) == 0 {
		return true
	}
	posPatterns, negPatterns := parseRuleFilePatterns(rule.Files)
	if len(files) == 0 {
		return len(posPatterns) == 0
	}
	for _, file := range files {
		fileSegs := strings.Split(filepath.ToSlash(file), "/")
		if fileMatchesRule(posPatterns, negPatterns, fileSegs) {
			return true
		}
	}
	return false
}

func parseRuleFilePatterns(patterns []string) ([][]string, [][]string) {
	var posPatterns [][]string
	var negPatterns [][]string
	for _, f := range patterns {
		f = strings.TrimSpace(f)
		negate := false
		if strings.HasPrefix(f, "!") {
			negate = true
			f = strings.TrimPrefix(f, "!")
		}
		switch f {
		case "$all":
			f = "**/*.go"
		case "$test":
			f = "**/*_test.go"
		}
		segs := strings.Split(filepath.ToSlash(f), "/")
		if negate {
			negPatterns = append(negPatterns, segs)
		} else {
			posPatterns = append(posPatterns, segs)
		}
	}
	return posPatterns, negPatterns
}

func fileMatchesRule(posPatterns, negPatterns [][]string, fileSegs []string) bool {
	for _, neg := range negPatterns {
		if util.MatchGlobSegments(neg, fileSegs) {
			return false
		}
	}
	if len(posPatterns) == 0 {
		return true
	}
	for _, pos := range posPatterns {
		if util.MatchGlobSegments(pos, fileSegs) {
			return true
		}
	}
	return false
}

func checkDepguardRule(rule config.DepguardRuleConfig, pkg string) (bool, string) {
	mode := strings.ToLower(rule.ListMode)
	if mode == "strict" {
		return checkStrictRule(rule, pkg)
	}
	return checkOriginalRule(rule, pkg)
}

func checkStrictRule(rule config.DepguardRuleConfig, pkg string) (bool, string) {
	for _, allowed := range rule.Allow {
		if isAllowedPackage(allowed, pkg) {
			return false, ""
		}
	}
	for _, d := range rule.Deny {
		if packageMatches(d.Pkg, pkg) {
			return true, d.Pkg
		}
	}
	return true, pkg
}

func checkOriginalRule(rule config.DepguardRuleConfig, pkg string) (bool, string) {
	for _, d := range rule.Deny {
		if !packageMatches(d.Pkg, pkg) {
			continue
		}
		if isOverrideByLongerAllow(rule.Allow, pkg, len(d.Pkg)) {
			continue
		}
		return true, d.Pkg
	}
	return false, ""
}

func isAllowedPackage(allowed, pkg string) bool {
	if allowed == "$gostd" {
		return !strings.ContainsRune(pkg, '.')
	}
	return packageMatches(allowed, pkg)
}

func packageMatches(pattern, pkg string) bool {
	if strings.HasSuffix(pattern, "$") {
		return pkg == strings.TrimSuffix(pattern, "$")
	}
	return strings.HasPrefix(pkg, pattern)
}

func isOverrideByLongerAllow(allow []string, pkg string, denyLen int) bool {
	for _, a := range allow {
		if packageMatches(a, pkg) && len(a) > denyLen {
			return true
		}
	}
	return false
}
