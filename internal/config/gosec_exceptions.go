// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"fmt"
	"slices"
	"strings"
)

// ExceptionRuleGosec names dated per-file gosec findings excused by the Security stage.
const ExceptionRuleGosec = "gosec"

// MaxGosecExceptionRules bounds the distinct G000..G999 identifiers of one entry.
const MaxGosecExceptionRules = 1000

func (e Exception) scannerExceptionProblem() string {
	if problem := e.codespellProblem(); problem != "" {
		return problem
	}
	return e.gosecProblem()
}

func (e Exception) gosecProblem() string {
	if e.Rule != ExceptionRuleGosec {
		if len(e.GosecRules) > 0 {
			return "gosec_rules is only valid for rule gosec"
		}
		return ""
	}
	if e.Path == "" || e.Glob != "" {
		return "rule gosec must name one file by path, not a glob"
	}
	if e.Category == "" || e.Entered == "" {
		return "rule gosec requires category and entered fields"
	}
	if e.Fingerprint != "" || e.Lines != "" {
		return "rule gosec must not set fingerprint or lines"
	}
	return gosecRulesProblem(e.GosecRules)
}

func gosecRulesProblem(ids []string) string {
	if len(ids) == 0 || len(ids) > MaxGosecExceptionRules {
		return fmt.Sprintf("gosec_rules requires 1..%d rule ids", MaxGosecExceptionRules)
	}
	seen := make(map[string]bool, len(ids))
	for i := 0; i < len(ids) && i < MaxGosecExceptionRules; i++ {
		id := ids[i]
		if len(id) != 4 || id[0] != 'G' || strings.Trim(id[1:], "0123456789") != "" {
			return fmt.Sprintf("gosec_rules id %q must be G plus three digits", id)
		}
		if seen[id] {
			return fmt.Sprintf("gosec_rules repeats %s", id)
		}
		seen[id] = true
	}
	return ""
}

// MatchesGosec matches exactly one repository path and one declared gosec rule id.
func (e Exception) MatchesGosec(path, rule string) bool {
	return e.Rule == ExceptionRuleGosec && e.Path == path && slices.Contains(e.GosecRules, rule)
}
