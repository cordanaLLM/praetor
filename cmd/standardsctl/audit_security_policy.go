// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/cordanaLLM/praetor/internal/adopt"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/contextopt"
)

// securityContactChannel recognizes an email reporting destination or an HTTPS security
// contact. A bare heading or an instruction to contact unnamed maintainers is insufficient.
var securityContactChannel = regexp.MustCompile(`(?im)(?:mailto:|(?:security[ \t]+contact|contact|e-?mail)(?:[ \t]+(?:us|at|to))*[ \t]*:?)[ \t]*<?[a-z0-9.!#$%&'*+/=?^_{|}~-]+@[a-z0-9-]+(?:\.[a-z0-9-]+)+|security[ \t]+contact[ \t]*:[ \t]*https://[^\s<>]+`)

// affirmativeReporting matches an affirmative instruction to report via private vulnerability reporting.
var affirmativeReporting = regexp.MustCompile(`(?i)\b(?:use|using|report(?:ed|ing)?|submit(?:ted|ting)?|file|filing|via|through|enable[d]?|choose|navigate|open|visit)\b[^.!?;]*\b(?:github'?s\s+)?private\s+vulnerability\s+reporting\b|\bprivate\s+vulnerability\s+reporting\b[^.!?;]*\b(?:is\s+enabled|form|tab)\b|\bprivate\s+vulnerability\s+reporting\s*:[^.!?;]*\b(?:open|choose|click|navigate|visit|report|use)\b`)

// negatedReporting matches a negation directly governing private vulnerability reporting (at
// most two words between them, so "do not hesitate to use" stays affirmative),
// or a statement that the reporting channel is disabled or unavailable.
var negatedReporting = regexp.MustCompile(`(?i)\b(?:do\s+not|don'?t|never|cannot|no\b|without|not\b|disable[d]?|unsupported|not\s+supported)\b(?:\s+[^\s.!?;,:]+){0,2}\s+(?:github'?s\s+)?private\s+vulnerability\s+reporting\b|\b(?:github'?s\s+)?private\s+vulnerability\s+reporting\b\s+(?:(?:is|are)\s+(?:not(?:\s+supported)?|disabled|unsupported|unavailable)|disabled|unsupported|not\s+supported|unavailable)\b`)

// actionableAdvisoryLink matches a URL or markdown link to GitHub's advisory reporting form.
var actionableAdvisoryLink = regexp.MustCompile(`(?i)https://github\.com/[^\s/]+/[^\s/]+/security/advisories(?:/new)?|/security/advisories(?:/new)?`)

// affirmativeConfidentialIssue matches an affirmative instruction to open a confidential issue.
var affirmativeConfidentialIssue = regexp.MustCompile(`(?im)\b(?:open|create|submit|file|use)\b[^.!?;]*\bconfidential\s+issue(?:s)?\b|\bconfidential\s+issue(?:s)?\b[^.!?;]*\b(?:in this repository|to report|for security)\b`)

// policyChannels summarizes the private reporting channels detected in a security policy.
type policyChannels struct {
	private      bool
	confidential bool
	contact      bool
}

// hasAny reports whether at least one actionable private reporting channel is present.
func (c policyChannels) hasAny() bool {
	return c.private || c.confidential || c.contact
}

// matchesForge reports whether the policy channels are consistent with the declared forge.
func (c policyChannels) matchesForge(forge config.Forge) bool {
	if forge == "" || forge == config.ForgeGitHub {
		return true
	}
	return !c.private || c.confidential || c.contact
}

// evaluatePolicyChannels inspects policy content for affirmative private reporting channels.
func evaluatePolicyChannels(content string) policyChannels {
	text := strings.ToLower(strings.Join(strings.Fields(content), " "))
	private := isAffirmativePrivateReporting(text, content)
	return policyChannels{
		private:      private,
		confidential: !private && affirmativeConfidentialIssue.MatchString(content),
		contact:      securityContactChannel.MatchString(content),
	}
}

// isAffirmativePrivateReporting checks whether the policy contains an affirmative instruction
// to use private vulnerability reporting or an actionable link to its advisory form.
func isAffirmativePrivateReporting(text, content string) bool {
	if !strings.Contains(text, "private vulnerability reporting") {
		return false
	}
	if negatedReporting.MatchString(text) {
		return false
	}
	return affirmativeReporting.MatchString(text) || actionableAdvisoryLink.MatchString(content)
}

// auditSecurityPolicy extends community health in the existing audit path. Declined
// policies keep their existing contract; unread settings are named and never fail offline.
func auditSecurityPolicy(ctx context.Context, manifest *config.Manifest, root string, live actionsForge) error {
	decline, err := adopt.AuditDecline(manifest, "security-policy")
	if err != nil {
		return fmt.Errorf("[FAIL] Community health: SECURITY.md: %w", err)
	}
	if decline.Declined {
		fmt.Println("[INFO] Community health: SECURITY.md declined; private vulnerability reporting setting not read.")
		return nil
	}
	path, content, err := readSecurityPolicy(ctx, root)
	if err != nil {
		return err
	}
	channels := evaluatePolicyChannels(content)
	if !channels.hasAny() {
		return fmt.Errorf("[FAIL] Community health: %s names no private reporting channel; name private vulnerability reporting or a security contact", path)
	}
	forge := config.Forge("")
	if manifest != nil {
		forge = manifest.Repository.Forge
	}
	if !channels.matchesForge(forge) {
		return fmt.Errorf("[FAIL] Community health: %s names GitHub private vulnerability reporting, but repository.forge is %s", path, forge)
	}
	fmt.Printf("[PASS] Community health: %s names a private reporting channel.\n", path)
	if !channels.private {
		channel := "a security contact"
		if channels.confidential {
			channel = "confidential issues"
		}
		fmt.Printf("[INFO] %s: private vulnerability reporting setting not read: policy uses %s.\n", path, channel)
		return nil
	}
	return auditPrivateVulnerabilityReporting(ctx, path, live)
}

// readSecurityPolicy reads the first policy in GitHub's community-file search order through
// the existing bounded, context-aware reader; an unreadable file never becomes an absence.
func readSecurityPolicy(ctx context.Context, root string) (string, string, error) {
	paths := [...]string{".github/SECURITY.md", "SECURITY.md", "docs/SECURITY.md"}
	for i := 0; i < len(paths); i++ {
		data, exists, err := contextopt.ObserveSnapshotIn(ctx, root, paths[i])
		if err != nil {
			return "", "", fmt.Errorf("[FAIL] Community health: read %s: %w", paths[i], err)
		}
		if exists {
			return paths[i], string(data), nil
		}
	}
	return "", "", fmt.Errorf("[FAIL] Community health: SECURITY.md missing; add .github/SECURITY.md, SECURITY.md or docs/SECURITY.md with a private reporting channel")
}

// auditPrivateVulnerabilityReporting reports unread settings separately from a verified
// disabled setting, which contradicts a policy directing reporters to that channel.
func auditPrivateVulnerabilityReporting(ctx context.Context, path string, live actionsForge) error {
	if live.reporting == nil {
		fmt.Printf("[SKIP] %s: private vulnerability reporting setting not read: %s\n", path, live.notMade)
		return nil
	}
	enabled, err := live.reporting.PrivateVulnerabilityReporting(ctx)
	if err != nil {
		fmt.Printf("[SKIP] %s: private vulnerability reporting setting not read: %v\n", path, err)
		return nil
	}
	if !enabled {
		return fmt.Errorf("[FAIL] Community health: %s names private vulnerability reporting, but the forge setting is enabled=false (mismatch)", path)
	}
	fmt.Printf("[PASS] %s: private vulnerability reporting setting enabled=true.\n", path)
	return nil
}
