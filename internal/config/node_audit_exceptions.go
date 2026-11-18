// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"regexp"
	"time"
)

// ExceptionRuleNodeAudit names per-advisory exceptions consumed by the Node security stage.
const ExceptionRuleNodeAudit = "node-audit"

var ghsaIDPattern = regexp.MustCompile(`^GHSA-[23456789cfghjmpqrvwx]{4}-[23456789cfghjmpqrvwx]{4}-[23456789cfghjmpqrvwx]{4}$`)

// ValidGHSA reports whether id is a complete GitHub security advisory identifier.
func ValidGHSA(id string) bool {
	return ghsaIDPattern.MatchString(id)
}

// nodeAuditProblem confines a Node exception to one advisory in the root Node manifest.
func (e Exception) nodeAuditProblem() string {
	if e.Rule != ExceptionRuleNodeAudit {
		if e.GHSA != "" {
			return "ghsa is only valid for rule node-audit"
		}
		return ""
	}
	if !ValidGHSA(e.GHSA) {
		return "rule node-audit requires one complete GHSA id in ghsa"
	}
	if e.Category == "" || e.Entered == "" {
		return "rule node-audit requires category and entered fields"
	}
	return e.nodeAuditScopeProblem()
}

// nodeAuditScopeProblem rejects additional selectors that could broaden an advisory exception.
func (e Exception) nodeAuditScopeProblem() string {
	if e.Path != "package.json" || e.Glob != "" {
		return "rule node-audit requires path: package.json and no glob"
	}
	if e.Function != "" || e.Fingerprint != "" || e.Lines != "" {
		return "rule node-audit must not set function, fingerprint or lines"
	}
	return ""
}

// NodeAuditExceptionFor reuses dated exception matching for one exact GHSA identifier.
func NodeAuditExceptionFor(entries []Exception, ghsa string, today time.Time) (live, expired *Exception) {
	return exceptionForMatch(entries, today, nil, func(e Exception) bool {
		return e.Rule == ExceptionRuleNodeAudit && e.Path == "package.json" && e.GHSA == ghsa
	})
}
