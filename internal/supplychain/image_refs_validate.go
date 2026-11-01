// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package supplychain

import (
	"regexp"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

var (
	// digestPinPattern matches a valid sha256 digest suffix on a container image reference.
	digestPinPattern = regexp.MustCompile(`@sha256:[0-9a-f]{64}(?:$|\s)`)

	// substitutionTokenPattern matches "@NAME@" substitution tokens filled at deploy time.
	substitutionTokenPattern = regexp.MustCompile(`@[A-Za-z_][A-Za-z0-9_]*@`)

	// ociRepoPattern matches a lowercase OCI repository path ("library/golang", "org/runner").
	ociRepoPattern = regexp.MustCompile(`^[a-z0-9]+(?:[._/-][a-z0-9]+)*$`)

	// ociTagPattern matches an OCI tag token ("1.4.2", "latest", "1.27-alpine").
	ociTagPattern = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._-]{0,127}$`)
)

func newImageRefFinding(file string, line int, ref string, unresolvable bool) ImageRefFinding {
	trimmed := strings.TrimSpace(ref)
	return ImageRefFinding{
		File:         util.NormalizeSlashes(file),
		Line:         line,
		Reference:    trimmed,
		RuleID:       ImageRefRuleID,
		Fingerprint:  ImageRefFingerprint(file, trimmed),
		Unresolvable: unresolvable,
	}
}

func hasDigestPin(ref string) bool {
	return digestPinPattern.MatchString(strings.TrimSpace(ref))
}

func isUnresolvableImageRef(ref string) bool {
	return strings.Contains(ref, "$") || strings.Contains(ref, "{{") || substitutionTokenPattern.MatchString(ref)
}

func isInvalidRegistryHost(ref string) bool {
	trimmed := strings.TrimPrefix(strings.TrimSpace(ref), "docker://")
	host, _, ok := strings.Cut(trimmed, "/")
	if !ok {
		return false
	}
	hostname, port, hasPort := strings.Cut(host, ":")
	if (hasPort && !isNumericPort(port)) || !isHostnameToken(hostname) {
		return false
	}
	lower := strings.ToLower(hostname)
	return lower == "invalid" || strings.HasSuffix(lower, ".invalid")
}

func appendImageRefFinding(findings []ImageRefFinding, file string, line int, ref string) []ImageRefFinding {
	trimmed := strings.TrimSpace(ref)
	if trimmed == "" || isInvalidRegistryHost(trimmed) || hasDigestPin(trimmed) {
		return findings
	}
	return append(findings, newImageRefFinding(file, line, trimmed, isUnresolvableImageRef(trimmed)))
}

// isUnpinnedQualifiedImageLiteral reports whether lit is a fully qualified container image
// reference ("<registry-host>/<repo>:<tag>") lacking an @sha256: digest pin. Plain host:port
// values without "/<repo>:<tag>" never match.
func isUnpinnedQualifiedImageLiteral(lit string) bool {
	if lit == "" || strings.ContainsAny(lit, " \t\r\n") || strings.Contains(lit, "://") {
		return false
	}
	host, rest, ok := strings.Cut(lit, "/")
	if !ok || !isRegistryHost(host) || isInvalidRegistryHost(lit) || hasDigestPin(rest) {
		return false
	}
	repo, tag, hasTag := strings.Cut(rest, ":")
	if !hasTag {
		return false
	}
	return ociRepoPattern.MatchString(repo) && ociTagPattern.MatchString(tag)
}

func isRegistryHost(host string) bool {
	if host == "" {
		return false
	}
	hostname, port, hasPort := strings.Cut(host, ":")
	if hasPort && !isNumericPort(port) {
		return false
	}
	if !isHostnameToken(hostname) {
		return false
	}
	return strings.Contains(hostname, ".") || hasPort || hostname == "localhost"
}

func isNumericPort(port string) bool {
	if len(port) == 0 || len(port) > 5 {
		return false
	}
	for i := 0; i < len(port); i++ {
		if port[i] < '0' || port[i] > '9' {
			return false
		}
	}
	return true
}

func isHostnameToken(h string) bool {
	if len(h) == 0 || h[0] == '.' || h[0] == '-' || h[len(h)-1] == '.' || h[len(h)-1] == '-' {
		return false
	}
	for i := 0; i < len(h) && i < maxLineLiterals; i++ {
		if !isHostnameByte(h[i]) {
			return false
		}
	}
	return true
}

func isHostnameByte(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '.' || c == '-'
}
