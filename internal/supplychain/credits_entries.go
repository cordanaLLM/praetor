// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package supplychain

// docs/credits.yaml is the one curated list of what praetor takes from others: one entry per
// third-party item, with its upstream, kind, relation, license and the repository paths that
// use it. The tables of the credits page are rendered from it (RenderCreditsPage), and
// CheckUpstreamCredits holds it to the repository: the dependency inventory
// (ReadCreditInventory), the paths it names, the personas and skills, and the declared
// exceptions list.

import (
	"context"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/util"
)

// AcknowledgementsList is the repository-relative curated credits list. Like AcknowledgementsFile,
// its name avoids "cred", which gosec's G101 hardcoded-credential rule reads in a constant's name.
const AcknowledgementsList = "docs/credits.yaml"

// Bounds of the credits list (HISS-02).
const (
	// maxCreditEntries bounds the entries, originals and downloads the list holds, each.
	maxCreditEntries = 1024
	// maxCreditValues bounds the paths, packages and match terms of one entry, each.
	maxCreditValues = 128
	// maxCreditText bounds one text field of an entry.
	maxCreditText = 2048
)

// The kinds of third-party item an entry names.
const (
	kindDependency    = "dependency"
	kindTool          = "tool"
	kindAction        = "action"
	kindImage         = "image"
	kindVendoredFile  = "vendored file"
	kindAdaptedCode   = "adapted code"
	kindInspiration   = "inspiration"
	kindSpecification = "specification"
	kindIntegration   = "integration"
	kindFontOrIcon    = "font or icon"
)

// creditKinds lists every kind an entry may name.
var creditKinds = []string{
	kindDependency, kindTool, kindAction, kindImage, kindVendoredFile, kindAdaptedCode,
	kindInspiration, kindSpecification, kindIntegration, kindFontOrIcon,
}

// The relations: what of the item reaches praetor. shipped is code in the release binaries or
// image; vendored is upstream text or code reproduced in this repository, under its own license;
// adapted rewrites upstream rules or structure in praetor's own words; inspired takes only the
// idea; used by CI runs only in the build, the hooks or CI; integrated is read, written or called
// by praetor, or installed by files praetor emits into an adopting repository.
const (
	relationShipped    = "shipped"
	relationVendored   = "vendored"
	relationAdapted    = "adapted"
	relationInspired   = "inspired"
	relationUsedByCI   = "used by CI"
	relationIntegrated = "integrated"
)

// creditRelations lists every relation an entry may name.
var creditRelations = []string{
	relationShipped, relationVendored, relationAdapted, relationInspired, relationUsedByCI, relationIntegrated,
}

// derivationRelations are the relations a persona or skill that declares metadata.derived_from
// may be credited with; vendored also requires its upstream license to be carried.
var derivationRelations = []string{relationVendored, relationAdapted, relationInspired}

// The license words an entry may name instead of an SPDX expression: unknown when the upstream
// license could not be verified, which needs a declared exception (config.ExceptionRuleCredits);
// proprietary for a closed product or service praetor integrates with under its provider's
// terms; none when the upstream states no license.
const (
	licenseUnknown     = "unknown"
	licenseProprietary = "proprietary"
	licenseNone        = "none"
)

// creditSection is one table of the credits page: the id an entry names and the level-two
// heading the table sits under.
type creditSection struct {
	id, heading string
}

// The section whose table answers each persona or skill derivation.
const sectionAdapted = "adapted"

// creditSections are the tables of the credits page, in page order.
var creditSections = []creditSection{
	{sectionAdapted, "Adapted work"},
	{"shipped", "Shipped in the binaries"},
	{"images", "Container and development images"},
	{"presets", "Documentation presets"},
	{"actions", "GitHub Actions"},
	{"integrations", "Integrations"},
	{"specifications", "Specifications and standards"},
	{"fonts", "Fonts and icons"},
	{"tooling", "Build and CI tooling"},
	{"development", "Development dependencies"},
}

// Credits is the decoded docs/credits.yaml.
type Credits struct {
	// Originals are the canonical personas and skills written for praetor, which therefore
	// declare no upstream.
	Originals []string `yaml:"originals"`
	// Downloads is the declared list of what CI fetches outside every manifest the inventory
	// reads, such as a release binary fetched with curl.
	Downloads []CreditDownload `yaml:"downloads"`
	// Entries are the third-party items, one each, in page order within each section.
	Entries []CreditEntry `yaml:"entries"`
}

// CreditDownload is one item CI fetches outside a manifest: its identifier, as an entry's
// packages name it, and the file that fetches it.
type CreditDownload struct {
	ID   string `yaml:"id"`
	Path string `yaml:"path"`
}

// CreditEntry is one third-party item.
type CreditEntry struct {
	// Name is the project name the page links.
	Name string `yaml:"name"`
	// URL is the upstream, an https URL.
	URL string `yaml:"url"`
	// Detail follows the link in the project cell, such as the versions a lock installs.
	Detail string `yaml:"detail,omitempty"`
	// Section is the id of the page table the entry is listed in (creditSections).
	Section string `yaml:"section"`
	// Kind is one of creditKinds.
	Kind string `yaml:"kind"`
	// Relation is one of creditRelations.
	Relation string `yaml:"relation"`
	// License is the SPDX license expression the upstream states, or licenseUnknown,
	// licenseProprietary or licenseNone.
	License string `yaml:"license"`
	// Notice follows the license in its cell: the copyright line, licensing history or what an
	// adopter receives.
	Notice string `yaml:"notice,omitempty"`
	// Artifact names the praetor artifact in the Adapted work table; that table alone has the
	// column, so only its entries carry one.
	Artifact string `yaml:"artifact,omitempty"`
	// Use says what praetor uses the item for, or in the Adapted work table what changed.
	Use string `yaml:"use"`
	// Paths are the repository files that use the item; each must still name it.
	Paths []string `yaml:"paths"`
	// Packages are the inventory items the entry answers, each written <ecosystem>:<identifier>
	// (packageEcosystems): go: a Go module or tool path, npm: and pypi: a package name, action:
	// an owner/repository, image: and feature: a repository, download: an id of the downloads
	// list. One answers an item of its ecosystem whose identifier it equals or continues after a
	// slash, so a name two ecosystems share (npm helm, the helm download) answers only one.
	Packages []string `yaml:"packages,omitempty"`
	// Match are terms a path names the item by. An entry with packages or match terms is looked
	// for by those alone, so a generic name (Go, Continue) never counts as a use; an entry with
	// neither is looked for by its name.
	Match []string `yaml:"match,omitempty"`
}

// label names the entry in a finding.
func (e CreditEntry) label(index int) string {
	return fmt.Sprintf("%s entries[%d] (%s)", AcknowledgementsList, index, e.Name)
}

// terms are the words a path that uses the entry names it by, lower-cased: the identifiers of its
// packages and its match terms, or its name when it declares neither.
func (e CreditEntry) terms() []string {
	terms := make([]string, 0, 1+len(e.Packages)+len(e.Match))
	for _, pkg := range e.Packages {
		terms = append(terms, strings.ToLower(packageIdentifier(pkg)))
	}
	for _, term := range e.Match {
		terms = append(terms, strings.ToLower(term))
	}
	if len(terms) == 0 {
		terms = append(terms, strings.ToLower(e.Name))
	}
	return terms
}

// packageEcosystems maps each inventory kind to the ecosystem an entry's package names it under.
var packageEcosystems = map[string]string{
	inventoryGoModule: "go", inventoryGoTool: "go", inventoryNPM: "npm", inventoryPyPI: "pypi",
	inventoryAction: "action", inventoryImage: "image", inventoryFeature: "feature", inventoryDownload: "download",
}

// packageKey is the package an entry answers item with: its ecosystem, a colon and its identifier.
func packageKey(item InventoryItem) string {
	return packageEcosystems[item.Kind] + ":" + item.ID
}

// packageIdentifier returns the identifier of a package an entry names, without its ecosystem.
func packageIdentifier(pkg string) string {
	_, id, _ := strings.Cut(pkg, ":")
	return id
}

// answers reports whether one of the entry's packages names item: the same ecosystem, and an
// identifier item's equals or continues after a slash, case aside.
func (e CreditEntry) answers(item InventoryItem) bool {
	key := strings.ToLower(packageKey(item))
	return slices.ContainsFunc(e.Packages, func(pkg string) bool {
		pkg = strings.ToLower(pkg)
		return key == pkg || strings.HasPrefix(key, pkg+"/")
	})
}

// packageProblem names why a package an entry names is refused, or returns "": it must be an
// ecosystem of packageEcosystems, a colon and a non-empty identifier.
func packageProblem(pkg string) string {
	ecosystem, id, found := strings.Cut(pkg, ":")
	if !found || !slices.Contains(packageEcosystemNames(), ecosystem) || id == "" {
		return fmt.Sprintf("package %q is not <ecosystem>:<identifier> with an ecosystem of %s", pkg, strings.Join(packageEcosystemNames(), ", "))
	}
	return ""
}

// packageEcosystemNames lists the ecosystems a package may name, sorted, once each.
func packageEcosystemNames() []string {
	names := slices.Sorted(maps.Values(packageEcosystems))
	return slices.Compact(names)
}

var (
	// creditURL is the form of an entry's URL: https, and nothing a Markdown link target ends at.
	creditURL = regexp.MustCompile(`^https://[^\s()<>|]+$`)
	// licenseTerm is one SPDX license or exception identifier, or a LicenseRef.
	licenseTerm = regexp.MustCompile(`^(LicenseRef-[A-Za-z0-9.-]+|[A-Za-z0-9][A-Za-z0-9.+-]*)$`)
)

// ReadCredits reads and decodes docs/credits.yaml at the top of the repository at root.
func ReadCredits(ctx context.Context, root string) (Credits, error) {
	data, err := readNoticeSource(ctx, root, AcknowledgementsList)
	if err != nil {
		return Credits{}, err
	}
	return DecodeCredits(data)
}

// DecodeCredits decodes one docs/credits.yaml document strictly, refusing an unknown key, a
// second document and a repeated key, and validates it (validateCredits).
func DecodeCredits(data []byte) (Credits, error) {
	var credits Credits
	if err := util.DecodeYAMLDocument(data, &credits, util.YAMLDocumentOptions{KnownFields: true}); err != nil {
		return Credits{}, fmt.Errorf("parse %s: %w", AcknowledgementsList, err)
	}
	if err := validateCredits(credits); err != nil {
		return Credits{}, err
	}
	return credits, nil
}

// validateCredits refuses a list the page and the gate could not apply as written: more than
// maxCreditEntries of a kind, an entry problem (entryProblem), an original or a download that
// names no clean repository path, and a repeated original.
func validateCredits(credits Credits) error {
	if len(credits.Entries) > maxCreditEntries || len(credits.Originals) > maxCreditEntries || len(credits.Downloads) > maxCreditEntries {
		return fmt.Errorf("%s holds more than %d entries, originals or downloads", AcknowledgementsList, maxCreditEntries)
	}
	for index, entry := range credits.Entries {
		if problem := entryProblem(entry); problem != "" {
			return fmt.Errorf("%s: %s", entry.label(index), problem)
		}
	}
	return validateCreditMarkers(credits)
}

// validateCreditMarkers refuses an original or a download that names no clean repository path,
// a repeated original, and a download without an id.
func validateCreditMarkers(credits Credits) error {
	for index, original := range credits.Originals {
		if !config.ValidRepositoryPath(original) || slices.Index(credits.Originals, original) != index {
			return fmt.Errorf("%s originals[%d] %q is not one clean repository path listed once", AcknowledgementsList, index, original)
		}
	}
	for index, download := range credits.Downloads {
		if !config.ValidRepositoryPath(download.Path) || textProblem(download.ID, true) != "" {
			return fmt.Errorf("%s downloads[%d] needs an id and one clean repository path", AcknowledgementsList, index)
		}
	}
	return nil
}

// entryProblem names why one entry is refused, or returns "".
func entryProblem(entry CreditEntry) string {
	switch {
	case textProblem(entry.Name, true) != "" || strings.ContainsAny(entry.Name, "[]"):
		return "name must be one line without | [ ]"
	case !creditURL.MatchString(entry.URL):
		return fmt.Sprintf("url %q must be an https URL", entry.URL)
	case !slices.ContainsFunc(creditSections, func(section creditSection) bool { return section.id == entry.Section }):
		return fmt.Sprintf("section %q is not a table of the page (known: %s)", entry.Section, strings.Join(creditSectionIDs(), ", "))
	case !slices.Contains(creditKinds, entry.Kind):
		return fmt.Sprintf("kind %q is not one of %s", entry.Kind, strings.Join(creditKinds, ", "))
	case !slices.Contains(creditRelations, entry.Relation):
		return fmt.Sprintf("relation %q is not one of %s", entry.Relation, strings.Join(creditRelations, ", "))
	case (entry.Section == sectionAdapted) != (entry.Artifact != ""):
		return "artifact is required in the adapted section and refused elsewhere"
	}
	if problem := licenseProblem(entry.License); problem != "" {
		return problem
	}
	return entryValuesProblem(entry)
}

// entryValuesProblem names why an entry's text fields, paths, packages or match terms are
// refused, or returns "".
func entryValuesProblem(entry CreditEntry) string {
	fields := [...][2]string{{"use", entry.Use}, {"detail", entry.Detail}, {"notice", entry.Notice}, {"artifact", entry.Artifact}}
	for _, field := range fields {
		if problem := textProblem(field[1], field[0] == "use"); problem != "" {
			return field[0] + " " + problem
		}
	}
	if len(entry.Paths) == 0 || len(entry.Paths) > maxCreditValues || len(entry.Packages) > maxCreditValues || len(entry.Match) > maxCreditValues {
		return fmt.Sprintf("must name 1..%d paths and at most %d packages and match terms", maxCreditValues, maxCreditValues)
	}
	return entryListsProblem(entry)
}

// entryListsProblem names why one of an entry's paths, packages or match terms is refused, or
// returns "".
func entryListsProblem(entry CreditEntry) string {
	for _, rel := range entry.Paths {
		if !config.ValidRepositoryPath(rel) || strings.ContainsAny(rel, "*?[]") {
			return fmt.Sprintf("path %q is not one clean repository file path", rel)
		}
	}
	for _, term := range append(slices.Clone(entry.Packages), entry.Match...) {
		if problem := textProblem(term, true); problem != "" {
			return fmt.Sprintf("package or match term %q %s", term, problem)
		}
	}
	for _, pkg := range entry.Packages {
		if problem := packageProblem(pkg); problem != "" {
			return problem
		}
	}
	return ""
}

// textProblem names why a text field is refused, or returns "": a required one empty, more than
// maxCreditText bytes, a line break or another control character, or a pipe, which ends a table
// cell.
func textProblem(value string, required bool) string {
	switch {
	case required && strings.TrimSpace(value) == "":
		return "is required"
	case len(value) > maxCreditText:
		return fmt.Sprintf("exceeds %d bytes", maxCreditText)
	case strings.ContainsFunc(value, func(r rune) bool { return r < ' ' || r == 0x7f }) || strings.Contains(value, "|"):
		return "must be one line without control characters or |"
	case strings.TrimSpace(value) != value:
		return "must not start or end with a space"
	}
	return ""
}

// licenseWords are the license values that are no SPDX expression.
var licenseWords = map[string]bool{licenseUnknown: true, licenseProprietary: true, licenseNone: true}

// deprecatedLicenseIDs are the license and exception identifiers SPDX License List 3.29.0
// (2026-09-16, https://spdx.org/licenses/, isDeprecatedLicenseId) marks deprecated. A GNU
// identifier without -only or -or-later is among them: it does not say whether later versions
// apply, which is the difference the upstream's notice states. The repository vendors no SPDX
// list, so the deprecated ones are held here; refresh them with a new list release.
var deprecatedLicenseIDs = map[string]bool{
	"AGPL-1.0": true, "AGPL-3.0": true, "BSD-2-Clause-FreeBSD": true, "BSD-2-Clause-NetBSD": true,
	"GFDL-1.1": true, "GFDL-1.2": true, "GFDL-1.3": true, "GPL-1.0": true, "GPL-1.0+": true,
	"GPL-2.0": true, "GPL-2.0+": true, "GPL-2.0-with-GCC-exception": true, "GPL-2.0-with-autoconf-exception": true,
	"GPL-2.0-with-bison-exception": true, "GPL-2.0-with-classpath-exception": true, "GPL-2.0-with-font-exception": true,
	"GPL-3.0": true, "GPL-3.0+": true, "GPL-3.0-with-GCC-exception": true, "GPL-3.0-with-autoconf-exception": true,
	"LGPL-2.0": true, "LGPL-2.0+": true, "LGPL-2.1": true, "LGPL-2.1+": true, "LGPL-3.0": true, "LGPL-3.0+": true,
	"Net-SNMP": true, "Nunit": true, "StandardML-NJ": true, "bzip2-1.0.5": true, "eCos-2.0": true, "wxWindows": true,
	"Nokia-Qt-exception-1.1": true,
}

// licenseProblem names why a license value is refused, or returns "": it is one of the license
// words, or an SPDX expression in which identifiers and AND, OR and WITH alternate and no
// identifier is deprecated (deprecatedLicenseIDs).
func licenseProblem(license string) string {
	if licenseWords[license] {
		return ""
	}
	fields := strings.FieldsFunc(license, func(r rune) bool { return r == '(' || r == ')' || r == ' ' })
	if len(fields)%2 == 0 || len(fields) > maxCreditValues || textProblem(license, true) != "" || !alternatesTerms(fields) {
		return fmt.Sprintf("license %q is not an SPDX expression or one of %s, %s, %s", license, licenseUnknown, licenseProprietary, licenseNone)
	}
	if index := slices.IndexFunc(fields, func(field string) bool { return deprecatedLicenseIDs[field] }); index >= 0 {
		return fmt.Sprintf("license %q names %s, which the SPDX License List deprecates; write the identifier the upstream states, "+
			"for a GNU license the -only or -or-later form", license, fields[index])
	}
	return ""
}

// alternatesTerms reports whether fields hold a license identifier at every even place and an
// operator at every odd one.
func alternatesTerms(fields []string) bool {
	for index, field := range fields {
		term := licenseTerm.MatchString(field) && !licenseOperators[field]
		if term != (index%2 == 0) || (!term && !licenseOperators[field]) {
			return false
		}
	}
	return true
}

// creditSectionIDs lists the section ids in page order.
func creditSectionIDs() []string {
	ids := make([]string, 0, len(creditSections))
	for _, section := range creditSections {
		ids = append(ids, section.id)
	}
	return ids
}
