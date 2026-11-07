// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package bestpractices

import (
	"regexp"
	"strings"
)

// httpURLPattern matches an http:// or https:// URL containing a dotted host.
var httpURLPattern = regexp.MustCompile(`https?://[^/\s.]+\.[^\s]+`)

// activeCriteriaForClaims builds an effective criteria map for the given claims, merging higher badge level rules when targeted.
func activeCriteriaForClaims(cat *Catalog, claims []statusClaim) map[string]Criterion {
	active := make(map[string]Criterion, len(cat.Criteria))
	for name, crit := range cat.Criteria {
		active[name] = crit
	}
	hasSilver, hasGold := claimLevels(cat, claims)
	if hasSilver || hasGold {
		mergeLevelCriteria(active, cat.ByLevel["1"])
	}
	if hasGold {
		mergeLevelCriteria(active, cat.ByLevel["2"])
	}
	return active
}

// claimLevels inspects claims to determine whether silver (level 1) or gold (level 2) criteria rules should be activated.
func claimLevels(cat *Catalog, claims []statusClaim) (hasSilver, hasGold bool) {
	for i := 0; i < len(claims) && i < maxObjectMembers; i++ {
		name := claims[i].criterion
		if base, ok := cat.Criteria[name]; ok {
			hasSilver = hasSilver || base.Level == "1"
			hasGold = hasGold || base.Level == "2"
		}
		for level, levelMap := range cat.ByLevel {
			if _, ok := levelMap[name]; !ok {
				continue
			}
			hasSilver = hasSilver || level == "1"
			hasGold = hasGold || level == "2"
		}
	}
	return hasSilver, hasGold
}

// mergeLevelCriteria merges higher-level criteria rules into the destination map.
func mergeLevelCriteria(dst, levelMap map[string]Criterion) {
	for name, higher := range levelMap {
		prev, exists := dst[name]
		if !exists {
			dst[name] = higher
			continue
		}
		higher.NAAllowed = prev.NAAllowed || higher.NAAllowed
		higher.NAJustificationRequired = prev.NAJustificationRequired || higher.NAJustificationRequired
		higher.MetJustificationRequired = prev.MetJustificationRequired || higher.MetJustificationRequired
		higher.MetURLRequired = prev.MetURLRequired || higher.MetURLRequired
		dst[name] = higher
	}
}

// evaluateClaim evaluates a single status claim against its criterion specification and returns any findings.
func evaluateClaim(claim statusClaim, justification string, criteria map[string]Criterion) []Finding {
	spec, known := criteria[claim.criterion]
	if !known {
		return []Finding{{
			Criterion: claim.criterion,
			Status:    claim.status,
			Rule:      RuleUnknownCriterion,
			Detail:    "criterion does not exist in criteria.yml or baseline_criteria.yml",
		}}
	}
	if !claim.validString || !isValidStatus(claim.status) {
		return []Finding{{
			Criterion: claim.criterion,
			Status:    claim.status,
			Rule:      RuleInvalidStatus,
			Detail:    "status must be one of Met, Unmet, N/A, ?",
		}}
	}
	switch claim.status {
	case "N/A":
		return evaluateNA(claim.criterion, justification, spec)
	case "Met":
		return evaluateMet(claim.criterion, justification, spec)
	default:
		return nil
	}
}

// evaluateNA checks N/A status requirements including allowance and justification.
func evaluateNA(criterion, justification string, spec Criterion) []Finding {
	var findings []Finding
	if !spec.NAAllowed {
		findings = append(findings, Finding{
			Criterion: criterion,
			Status:    "N/A",
			Rule:      RuleNANotAllowed,
			Detail:    "N/A is not allowed when na_allowed is false",
		})
	}
	if spec.NAJustificationRequired && strings.TrimSpace(justification) == "" {
		findings = append(findings, Finding{
			Criterion: criterion,
			Status:    "N/A",
			Rule:      RuleNAJustificationRequired,
			Detail:    "N/A requires a non-blank justification when na_justification_required is true",
		})
	}
	return findings
}

// evaluateMet checks Met status requirements including required justification and URL presence.
func evaluateMet(criterion, justification string, spec Criterion) []Finding {
	var findings []Finding
	if spec.MetJustificationRequired && strings.TrimSpace(justification) == "" {
		findings = append(findings, Finding{
			Criterion: criterion,
			Status:    "Met",
			Rule:      RuleMetJustificationRequired,
			Detail:    "Met requires a non-blank justification when met_justification_required is true",
		})
	}
	if spec.MetURLRequired && !containsHTTPURL(justification) {
		findings = append(findings, Finding{
			Criterion: criterion,
			Status:    "Met",
			Rule:      RuleMetURLRequired,
			Detail:    "Met requires an http(s) URL in the justification when met_url_required is true",
		})
	}
	return findings
}

// isValidStatus reports whether the status string is one of the valid OpenSSF status values.
func isValidStatus(status string) bool {
	return status == "Met" || status == "Unmet" || status == "N/A" || status == "?"
}

// containsHTTPURL reports whether the justification contains a valid http:// or https:// URL.
func containsHTTPURL(justification string) bool {
	trimmed := strings.TrimSpace(justification)
	if trimmed == "" || strings.HasPrefix(trimmed, "// ") {
		return false
	}
	return httpURLPattern.MatchString(trimmed)
}
