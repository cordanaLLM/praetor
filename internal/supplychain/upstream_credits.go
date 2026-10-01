// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package supplychain

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/cordanaLLM/praetor/internal/compiler"
	"github.com/cordanaLLM/praetor/internal/contextopt"
)

// A persona or skill taken from an upstream project names it in its front matter as
// metadata.derived_from: "<url> (<SPDX license>)" (compiler.CanonicalAssetUpstreams;
// docs/adr/0014-operator-neutral-defaults.md, section 8). The "Adapted work" table of the
// credits page answers each declaration with a row that links the same URL, names the declaring
// file, says how much was taken and states the same license. Upstream text or code that is
// copied must also carry its license: REUSE.toml labels the file with it and LICENSES/ holds its
// text. CheckUpstreamCredits holds the declarations, the rows and the license files together.

const (
	// adaptedWorkHeading opens the section of AcknowledgementsFile whose table answers each
	// declared upstream.
	adaptedWorkHeading = "## Adapted work"
	// adaptedWorkColumns are the cells of one row: project, artifact, relation, what changed and
	// license.
	adaptedWorkColumns = 5
	// licensesDir holds the full text of every license REUSE names, one <id>.txt each.
	licensesDir = "LICENSES"
)

// The relation column: copied reproduces upstream text or code, adapted rewrites upstream rules
// or structure in praetor's own words, inspired takes only the idea.
const (
	relationCopied   = "copied"
	relationAdapted  = "adapted"
	relationInspired = "inspired"
)

var (
	// derivedFromValue is the declaration form: an https URL, one space and a parenthesised SPDX
	// license expression.
	derivedFromValue = regexp.MustCompile(`^(https://[^\s()]+) \((.+)\)$`)
	// codeSpan captures the text of one inline code span, the form a row names an artifact in.
	codeSpan = regexp.MustCompile("`([^`]+)`")
)

// UpstreamCreditSources are what CheckUpstreamCredits compares.
type UpstreamCreditSources struct {
	// Credits is AcknowledgementsFile.
	Credits string
	// Reuse is ReuseFile, empty when the repository has none.
	Reuse string
	// Upstreams are the canonical personas and skills that declare metadata.derived_from.
	Upstreams []compiler.AssetUpstream
	// LicenseTexts reports, for each license identifier a declaration names, whether
	// LICENSES/<id>.txt exists.
	LicenseTexts map[string]bool
}

// derivation is one parsed metadata.derived_from declaration.
type derivation struct {
	rel, url, license string
}

// adaptedRow is one data row of the "Adapted work" table.
type adaptedRow struct {
	line                   int
	url, relation, license string
	artifacts              []string
}

// ReadUpstreamCreditSources reads the credits page, REUSE.toml, the upstream every canonical
// persona and skill declares, and whether LICENSES/ holds the text of each license they name,
// from the repository at root.
func ReadUpstreamCreditSources(ctx context.Context, root string) (UpstreamCreditSources, error) {
	credits, err := readNoticeSource(ctx, root, AcknowledgementsFile)
	if err != nil {
		return UpstreamCreditSources{}, err
	}
	reuse, _, err := contextopt.ObserveSnapshotIn(ctx, root, ReuseFile)
	if err != nil {
		return UpstreamCreditSources{}, fmt.Errorf("read %s: %w", ReuseFile, err)
	}
	upstreams, err := compiler.CanonicalAssetUpstreams(ctx, root)
	if err != nil {
		return UpstreamCreditSources{}, err
	}
	texts, err := readLicenseTexts(ctx, root, upstreams)
	if err != nil {
		return UpstreamCreditSources{}, err
	}
	return UpstreamCreditSources{Credits: string(credits), Reuse: string(reuse), Upstreams: upstreams, LicenseTexts: texts}, nil
}

// readLicenseTexts reports, for each license identifier upstreams declare, whether
// LICENSES/<id>.txt exists below root. A declaration that does not parse names none here;
// CheckUpstreamCredits reports it.
func readLicenseTexts(ctx context.Context, root string, upstreams []compiler.AssetUpstream) (map[string]bool, error) {
	texts := map[string]bool{}
	for _, upstream := range upstreams {
		parsed, err := parseDerivation(upstream)
		if err != nil {
			continue
		}
		for _, id := range licenseTerms(parsed.license) {
			if _, seen := texts[id]; seen {
				continue
			}
			_, exists, err := contextopt.ObserveSnapshotIn(ctx, root, licensesDir+"/"+id+".txt")
			if err != nil {
				return nil, fmt.Errorf("read %s/%s.txt: %w", licensesDir, id, err)
			}
			texts[id] = exists
		}
	}
	return texts, nil
}

// CheckUpstreamCredits fails when a declared upstream and the credits page disagree: a
// declaration that does not parse; one the "Adapted work" table answers with no row linking its
// URL and naming the declaring file; a row stating another license or a relation outside
// copied, adapted and inspired; a copied upstream whose license REUSE.toml does not put on the
// file or whose text LICENSES/ lacks; and a row naming a canonical persona or skill that
// declares no upstream or another one. Every finding is returned, joined.
func CheckUpstreamCredits(sources UpstreamCreditSources) error {
	rows, err := adaptedWorkRows(sources.Credits)
	if err != nil {
		return err
	}
	tables, err := ReuseAnnotationTables(sources.Reuse)
	if err != nil {
		return err
	}
	declared := map[string]derivation{}
	var findings []error
	for _, upstream := range sources.Upstreams {
		parsed, err := parseDerivation(upstream)
		if err != nil {
			findings = append(findings, err)
			continue
		}
		declared[parsed.rel] = parsed
		findings = append(findings, checkDerivationCredit(parsed, rows, tables, sources.LicenseTexts))
	}
	return errors.Join(append(findings, checkCreditedAssets(rows, declared)...)...)
}

// parseDerivation splits one declaration into its URL and license expression.
func parseDerivation(upstream compiler.AssetUpstream) (derivation, error) {
	match := derivedFromValue.FindStringSubmatch(upstream.DerivedFrom)
	if match == nil || strings.TrimSpace(match[2]) != match[2] || len(licenseTerms(match[2])) == 0 {
		return derivation{}, fmt.Errorf("%s: metadata.%s %q is not \"<https URL> (<SPDX license>)\"",
			upstream.Rel, compiler.DerivedFromKey, upstream.DerivedFrom)
	}
	return derivation{rel: upstream.Rel, url: match[1], license: match[2]}, nil
}

// checkDerivationCredit returns the finding for one declaration, or nil when its row answers it.
func checkDerivationCredit(parsed derivation, rows []adaptedRow, tables []string, texts map[string]bool) error {
	index := slices.IndexFunc(rows, func(row adaptedRow) bool {
		return row.url == parsed.url && slices.Contains(row.artifacts, parsed.rel)
	})
	if index < 0 {
		return fmt.Errorf("%s declares metadata.%s %s, and the %q table of %s has no row linking %s and naming `%s`",
			parsed.rel, compiler.DerivedFromKey, parsed.url, strings.TrimPrefix(adaptedWorkHeading, "## "), AcknowledgementsFile, parsed.url, parsed.rel)
	}
	row := rows[index]
	if row.license != parsed.license {
		return fmt.Errorf("%s:%d credits `%s` under %q, and the file declares %q; state the license the upstream names, first in the license cell",
			AcknowledgementsFile, row.line, parsed.rel, row.license, parsed.license)
	}
	switch row.relation {
	case relationAdapted, relationInspired:
		return nil
	case relationCopied:
		return checkCopiedLicense(parsed, tables, texts)
	}
	return fmt.Errorf("%s:%d gives `%s` the relation %q; write %s, %s or %s",
		AcknowledgementsFile, row.line, parsed.rel, row.relation, relationCopied, relationAdapted, relationInspired)
}

// checkCopiedLicense returns the finding for a copied upstream whose license is not carried:
// REUSE.toml must label the file with every license term, and LICENSES/ must hold each text.
func checkCopiedLicense(parsed derivation, tables []string, texts map[string]bool) error {
	var findings []error
	for _, id := range licenseTerms(parsed.license) {
		if !texts[id] {
			findings = append(findings, fmt.Errorf("%s copies upstream text under %s, and %s/%s.txt does not exist; add the license text",
				parsed.rel, id, licensesDir, id))
		}
		if !ReuseLabels(tables, parsed.rel, id) {
			findings = append(findings, fmt.Errorf("%s copies upstream text under %s, and %s does not label it %s; add an override annotation with the upstream copyright after every table that covers it",
				parsed.rel, id, ReuseFile, id))
		}
	}
	return errors.Join(findings...)
}

// checkCreditedAssets returns a finding for every row that names a canonical persona or skill
// which declares no upstream, or another one than the row links.
func checkCreditedAssets(rows []adaptedRow, declared map[string]derivation) []error {
	var findings []error
	for _, row := range rows {
		for _, artifact := range row.artifacts {
			if !isCanonicalAsset(artifact) {
				continue
			}
			parsed, ok := declared[artifact]
			switch {
			case !ok:
				findings = append(findings, fmt.Errorf("%s:%d credits `%s` to %s, and that file declares no metadata.%s; add \"%s (%s)\" to its front matter",
					AcknowledgementsFile, row.line, artifact, row.url, compiler.DerivedFromKey, row.url, row.license))
			case parsed.url != row.url:
				findings = append(findings, fmt.Errorf("%s:%d credits `%s` to %s, and that file declares %s",
					AcknowledgementsFile, row.line, artifact, row.url, parsed.url))
			}
		}
	}
	return findings
}

// isCanonicalAsset reports whether artifact names a file below the canonical persona or skill
// directory, the files CanonicalAssetUpstreams reads.
func isCanonicalAsset(artifact string) bool {
	return strings.HasPrefix(artifact, compiler.CanonicalAgentsRel+"/") || strings.HasPrefix(artifact, compiler.CanonicalSkillsRel+"/")
}

// adaptedWorkRows returns the data rows of the table in the "Adapted work" section of credits:
// every table row after the section heading and before the next level-two heading, less the
// header and delimiter rows. A data row without exactly adaptedWorkColumns cells is an error.
func adaptedWorkRows(credits string) ([]adaptedRow, error) {
	section, first, err := creditsSection(credits, adaptedWorkHeading)
	if err != nil {
		return nil, err
	}
	var rows []adaptedRow
	header := true
	for index, text := range section {
		line := strings.TrimSpace(text)
		switch {
		case !strings.HasPrefix(line, "|"):
			header = true
		case isDelimiterRow(line):
			header = false
		case !header:
			row, err := parseAdaptedRow(first+index, line)
			if err != nil {
				return nil, err
			}
			rows = append(rows, row)
		}
	}
	return rows, nil
}

// creditsSection returns the lines of the credits page section that heading, a level-two
// heading line, opens: from the line after it to the next level-two heading or the end. first
// is the 1-based line number of the first returned line. A page without the heading has an empty
// section.
func creditsSection(credits, heading string) (section []string, first int, err error) {
	lines, err := splitNoticeLines(credits)
	if err != nil {
		return nil, 0, fmt.Errorf("parse %s: %w", AcknowledgementsFile, err)
	}
	start := slices.IndexFunc(lines, func(line string) bool { return strings.TrimSpace(line) == heading })
	if start < 0 {
		return nil, 0, nil
	}
	end := start + 1
	for end < len(lines) && !strings.HasPrefix(lines[end], "## ") {
		end++
	}
	return lines[start+1 : end], start + 2, nil
}

// parseAdaptedRow reads one data row, line its 1-based line number.
func parseAdaptedRow(line int, text string) (adaptedRow, error) {
	cells := tableCells(text)
	if len(cells) != adaptedWorkColumns {
		return adaptedRow{}, fmt.Errorf("%s:%d: an %q row holds %d cells, want %d (project, artifact, relation, what changed, license)",
			AcknowledgementsFile, line, strings.TrimPrefix(adaptedWorkHeading, "## "), len(cells), adaptedWorkColumns)
	}
	row := adaptedRow{line: line, relation: strings.TrimSpace(cells[2])}
	if link := creditsLink.FindStringSubmatch(cells[0]); link != nil {
		row.url = link[2]
	}
	for _, span := range codeSpan.FindAllStringSubmatch(cells[1], -1) {
		row.artifacts = append(row.artifacts, span[1])
	}
	license, _, _ := strings.Cut(cells[4], ",")
	row.license = strings.TrimSpace(license)
	return row, nil
}
