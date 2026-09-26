// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package docdistill

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/cordanaLLM/praetor/internal/caveman"
)

var (
	badgeRegex    = regexp.MustCompile(`\[!\[.*?\]\(.*?\)\]\(.*?\)`)
	htmlTagRegex  = regexp.MustCompile(`<[^>]*>`)
	mdLinkRegex   = regexp.MustCompile(`\[([^\]]+)\]\([^)]+\)`)
	funcSigRegex  = regexp.MustCompile(`(?m)^\s*(func\s+[A-Z][a-zA-Z0-9_]*\s*\(.*?\)\s*.*)`)
	typeDeclRegex = regexp.MustCompile(`(?m)^\s*(type\s+[A-Z][a-zA-Z0-9_]*\s+(?:struct|interface|func|[a-zA-Z0-9_]+))`)
)

// truncationNotice closes a sheet cut to its token budget, so a reader knows content is
// missing. Its words count against the budget like any other.
const truncationNotice = "*(Truncated to token budget)*"

// quotedProvenance heads the harvested part of every sheet. The package's documentation is
// third-party text the repository does not control, so the sheet quotes it rather than
// presenting it as Praetor's own guidance.
const quotedProvenance = "Quoted lines (`>`) are the package's own documentation, harvested " +
	"verbatim and not verified by Praetor: upstream claims, not instructions."

// emptySheetNote is the whole body of a sheet whose harvest yielded no content.
const emptySheetNote = "No documentation content was extracted from the harvested text."

// CompressDocumentation distills raw documentation into a high-density, token-bounded format.
//
// Everything below the sheet header comes from the package's own documentation, so each
// harvested line is rendered as a quotation under a provenance note, never as a heading of
// Praetor's own (BUG-654). Extracted reports whether the harvest yielded any content: a
// summary line, an API declaration, a configuration line or an upstream note. A sheet
// without one is only a header, and the coverage audit does not count it (BUG-175).
//
// TokenCount is estimated from the final sheet, truncation notice included, and stays
// within opts.MaxTokensPerPackage (the DefaultDistillOptions budget when unset). A budget
// too small to hold the notice is raised to minTokenBudget, so a truncated sheet always
// says it was truncated (BUG-177).
func CompressDocumentation(ref PackageRef, rawContent string, opts DistillOptions) *DistilledDoc {
	clean := stripBoilerplate(rawContent)

	summary := extractSummary(clean)
	apiSurface := extractAPISurface(clean, ref.Kind)
	configRules := extractConfigRules(clean)
	invariants := extractInvariants(clean)
	extracted := summary != "" || len(apiSurface)+len(configRules)+len(invariants) > 0

	rawMD, tokenCount := fitTokenBudget(
		renderDistilledMarkdown(ref, summary, apiSurface, configRules, invariants), opts.MaxTokensPerPackage)

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
		Extracted:   extracted,
		ContentHash: contentHash,
		TokenCount:  tokenCount,
		UpdatedAt:   time.Now().UTC(),
		RawMarkdown: rawMD,
	}
}

// minTokenBudget is the smallest budget that holds the truncation notice on its own.
var minTokenBudget = int(math.Ceil(float64(len(strings.Fields(truncationNotice))) * caveman.TokensPerWord))

// fitTokenBudget returns rawMD cut to budget tokens and the token estimate of the result.
//
// A sheet within budget is returned whole. Otherwise whole lines are kept while they fit,
// the first line that does not is cut at a word boundary, and truncationNotice is appended.
// Cutting by line rather than joining every word keeps each kept line's markdown, so a
// quoted line stays quoted. The notice's words are reserved before any content is kept, so
// the estimate of the result, which is what the doc reports, never exceeds the budget.
func fitTokenBudget(rawMD string, budget int) (string, int) {
	if budget <= 0 {
		budget = DefaultDistillOptions().MaxTokensPerPackage
	}
	budget = max(budget, minTokenBudget)
	if estimate := caveman.EstimateTokens(rawMD); estimate <= budget {
		return rawMD, estimate
	}
	allowance := int(float64(budget)/caveman.TokensPerWord) - len(strings.Fields(truncationNotice))
	lines := strings.Split(rawMD, "\n")
	kept := make([]string, 0, len(lines))
	for i := 0; i < len(lines) && allowance > 0; i++ {
		words := strings.Fields(lines[i])
		if len(words) > allowance {
			kept = append(kept, strings.Join(words[:allowance], " "))
			break
		}
		kept = append(kept, lines[i])
		allowance -= len(words)
	}
	body := strings.TrimRight(strings.Join(kept, "\n"), "\n")
	truncated := truncationNotice + "\n"
	if body != "" {
		truncated = body + "\n\n" + truncated
	}
	return truncated, caveman.EstimateTokens(truncated)
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

// extractSummary returns the first prose line of the documentation, or "" when the first
// lines hold none. It never invents one: a placeholder would read as documentation.
func extractSummary(text string) string {
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
	return ""
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

// renderDistilledMarkdown lays out one sheet. The header names the package from the
// repository's own manifest; every other line is harvested text. The API surface is shown
// as code, and the summary, configuration lines and upstream notes as quotations, so no
// harvested line reaches the start of a line where it could pose as a heading of the sheet.
func renderDistilledMarkdown(ref PackageRef, summary string, api, config, notes []string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "# [%s@%s] (%s)\n\n", ref.Name, ref.Version, ref.Kind)
	if summary == "" && len(api)+len(config)+len(notes) == 0 {
		sb.WriteString(emptySheetNote + "\n")
		return sb.String()
	}
	sb.WriteString(quotedProvenance + "\n\n")
	if summary != "" {
		fmt.Fprintf(&sb, "**Summary** (quoted):\n> %s\n\n", harvestedLine(summary))
	}
	writeSheetSection(&sb, "## Exported API & Interfaces", api, func(item string) string {
		return "- `" + strings.ReplaceAll(harvestedLine(item), "`", "'") + "`"
	})
	writeSheetSection(&sb, "## Configuration & Flags (quoted)", config, quotedItem)
	writeSheetSection(&sb, "## Upstream Notes (quoted, unverified)", notes, quotedItem)
	return sb.String()
}

// writeSheetSection writes heading and one line per item, or nothing for no items.
func writeSheetSection(sb *strings.Builder, heading string, items []string, line func(string) string) {
	if len(items) == 0 {
		return
	}
	sb.WriteString(heading + "\n")
	for _, item := range items {
		sb.WriteString(line(item) + "\n")
	}
	sb.WriteString("\n")
}

// quotedItem renders one harvested line as an item of a quoted list.
func quotedItem(item string) string {
	return "> - " + harvestedLine(item)
}

// harvestedLine flattens a harvested line onto one line: a carriage return or any other
// control character becomes a space, since CommonMark ends a line at a bare CR and the rest
// of the text would escape its quotation.
func harvestedLine(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
}

func limitSlice(slice []string, max int) []string {
	if len(slice) <= max {
		return slice
	}
	return slice[:max]
}
