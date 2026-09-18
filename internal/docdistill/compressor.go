// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package docdistill

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/caveman"
)

var (
	badgeRegex    = regexp.MustCompile(`\[!\[.*?\]\(.*?\)\]\(.*?\)`)
	htmlTagRegex  = regexp.MustCompile(`<[^>]*>`)
	mdLinkRegex   = regexp.MustCompile(`\[([^\]]+)\]\([^)]+\)`)
	funcSigRegex  = regexp.MustCompile(`(?m)^\s*(func\s+[A-Z][a-zA-Z0-9_]*\s*\(.*?\)\s*.*)`)
	typeDeclRegex = regexp.MustCompile(`(?m)^\s*(type\s+[A-Z][a-zA-Z0-9_]*\s+(?:struct|interface|func|[a-zA-Z0-9_]+))`)
)

// CompressDocumentation distills raw documentation into a high-density, token-bounded format.
func CompressDocumentation(ref PackageRef, rawContent string, opts DistillOptions) *DistilledDoc {
	clean := stripBoilerplate(rawContent)

	summary := extractSummary(clean, ref.Name)
	apiSurface := extractAPISurface(clean, ref.Kind)
	configRules := extractConfigRules(clean)
	invariants := extractInvariants(clean)

	rawMD := renderDistilledMarkdown(ref, summary, apiSurface, configRules, invariants)

	// Enforce max token budget
	tokenBudget := opts.MaxTokensPerPackage
	if tokenBudget <= 0 {
		tokenBudget = 400
	}
	words := strings.Fields(rawMD)
	tokenEstimate := caveman.EstimateTokens(rawMD)
	if tokenEstimate > tokenBudget && len(words) > 0 {
		maxWords := int(float64(tokenBudget) / caveman.TokensPerWord)
		if maxWords < len(words) {
			truncatedWords := words[:maxWords]
			rawMD = strings.Join(truncatedWords, " ") + "\n\n*(Truncated to token budget)*"
			tokenEstimate = tokenBudget
		}
	}

	hash := sha256.Sum256([]byte(rawMD))
	contentHash := hex.EncodeToString(hash[:])

	return &DistilledDoc{
		PackageName: ref.Name,
		Version:     ref.Version,
		Kind:        ref.Kind,
		Summary:     summary,
		APISurface:  apiSurface,
		ConfigRules: configRules,
		Invariants:  invariants,
		ContentHash: contentHash,
		TokenCount:  tokenEstimate,
		UpdatedAt:   time.Now().UTC(),
		RawMarkdown: rawMD,
	}
}

func stripBoilerplate(text string) string {
	noBadges := badgeRegex.ReplaceAllString(text, "")
	noHTML := htmlTagRegex.ReplaceAllString(noBadges, "")
	lines := strings.Split(noHTML, "\n")
	var cleaned []string

	skipSection := false
	for i := 0; i < 5000 && i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		lower := strings.ToLower(line)

		if strings.HasPrefix(lower, "# ") || strings.HasPrefix(lower, "## ") {
			if strings.Contains(lower, "sponsor") || strings.Contains(lower, "license") ||
				strings.Contains(lower, "contributing") || strings.Contains(lower, "community") {
				skipSection = true
				continue
			} else {
				skipSection = false
			}
		}

		if skipSection || line == "" {
			continue
		}
		cleaned = append(cleaned, line)
	}
	return strings.Join(cleaned, "\n")
}

func extractSummary(text, pkgName string) string {
	lines := strings.Split(text, "\n")
	for i := 0; i < 50 && i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		plain := mdLinkRegex.ReplaceAllString(line, "$1")
		if len(plain) > 20 {
			return plain
		}
	}
	return fmt.Sprintf("Authoritative package %s for high-integrity workflows.", pkgName)
}

func extractAPISurface(text string, kind PackageKind) []string {
	var results []string
	switch kind {
	case KindGoModule:
		for _, match := range typeDeclRegex.FindAllString(text, 15) {
			results = append(results, strings.TrimSpace(match))
		}
		for _, match := range funcSigRegex.FindAllString(text, 20) {
			results = append(results, strings.TrimSpace(match))
		}
	case KindGitHubAction:
		results = extractActionInputs(text)
	}
	return limitSlice(results, 12)
}

func extractActionInputs(text string) []string {
	lines := strings.Split(text, "\n")
	var results []string
	inInputs := false
	for i := 0; i < 500 && i < len(lines); i++ {
		line := lines[i]
		switch strings.TrimSpace(line) {
		case "inputs:":
			inInputs = true
			continue
		case "outputs:", "runs:":
			inInputs = false
			continue
		}
		if inInputs && strings.HasPrefix(line, "  ") && !strings.HasPrefix(line, "    ") {
			results = append(results, strings.TrimSpace(strings.TrimSuffix(line, ":")))
		}
	}
	return results
}

func extractConfigRules(text string) []string {
	lines := strings.Split(text, "\n")
	var rules []string
	for i := 0; i < 500 && i < len(lines); i++ {
		l := lines[i]
		if strings.Contains(l, "--") || strings.Contains(l, "flag") || strings.Contains(l, "env:") {
			rules = append(rules, strings.TrimSpace(l))
		}
	}
	return limitSlice(rules, 8)
}

func extractInvariants(text string) []string {
	lines := strings.Split(text, "\n")
	var invs []string
	for i := 0; i < 500 && i < len(lines); i++ {
		l := strings.ToLower(lines[i])
		if strings.Contains(l, "must ") || strings.Contains(l, "warning") || strings.Contains(l, "note:") || strings.Contains(l, "deprecated") {
			invs = append(invs, strings.TrimSpace(lines[i]))
		}
	}
	return limitSlice(invs, 6)
}

func renderDistilledMarkdown(ref PackageRef, summary string, api, config, invs []string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "# [%s@%s] (%s)\n\n", ref.Name, ref.Version, ref.Kind)
	fmt.Fprintf(&sb, "**Summary**: %s\n\n", summary)

	if len(api) > 0 {
		sb.WriteString("## Exported API & Interfaces\n")
		for _, item := range api {
			fmt.Fprintf(&sb, "- `%s`\n", item)
		}
		sb.WriteString("\n")
	}

	if len(config) > 0 {
		sb.WriteString("## Configuration & Flags\n")
		for _, item := range config {
			fmt.Fprintf(&sb, "- %s\n", item)
		}
		sb.WriteString("\n")
	}

	if len(invs) > 0 {
		sb.WriteString("## Invariants & Gotchas\n")
		for _, item := range invs {
			fmt.Fprintf(&sb, "- %s\n", item)
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

func limitSlice(slice []string, max int) []string {
	if len(slice) <= max {
		return slice
	}
	return slice[:max]
}
