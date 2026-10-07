// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package supplychain

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/compiler"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/contextopt"
)

// CheckUpstreamCredits is the credits gate. It holds docs/credits.yaml (AcknowledgementsList) to the
// repository:
//
//   - every item the dependency inventory lists (ReadCreditInventory) and every download the
//     list declares has an entry whose packages name it;
//   - every path an entry, an original or a download names is a file that still names the item;
//   - a persona or skill taken from an upstream names it in its front matter as
//     metadata.derived_from: "<url> (<SPDX license>)" (compiler.CanonicalAssetUpstreams;
//     docs/adr/0014-operator-neutral-defaults.md, section 8), and an entry of a derivation
//     relation answers it with the same URL, the declaring file among its paths and the same
//     license. Upstream text that is vendored must also carry its license: REUSE.toml labels the
//     file with it and LICENSES/ holds its text. Every other canonical persona and skill is
//     listed under originals;
//   - an entry whose license could not be verified is written unknown and excused by an
//     unexpired entry of the declared exceptions list under config.ExceptionRuleCredits that
//     names one of its paths; an expired or a stale exception fails.

// derivedFromValue is the declaration form: an https URL, one space and a parenthesised SPDX
// license expression.
var derivedFromValue = regexp.MustCompile(`^(https://[^\s()]+) \((.+)\)$`)

// UpstreamCreditSources are what CheckUpstreamCredits compares.
type UpstreamCreditSources struct {
	// Credits is the decoded AcknowledgementsList.
	Credits Credits
	// Reuse is ReuseFile, empty when the repository has none.
	Reuse string
	// Assets are every canonical persona and skill, with the upstream each declares.
	Assets []compiler.AssetUpstream
	// LicenseTexts reports, for each license identifier a declaration names, whether
	// LICENSES/<id>.txt exists.
	LicenseTexts map[string]bool
	// Inventory is what the repository's manifests use (ReadCreditInventory).
	Inventory []InventoryItem
	// Exceptions are the entries of the declared exceptions list under
	// config.ExceptionRuleCredits.
	Exceptions []config.Exception
	// PathTexts holds, lower-cased, the text of every file the credits list names; a named path
	// that is no file in the repository is absent.
	PathTexts map[string]string
	// Today is the day exceptions expire against.
	Today time.Time
}

// derivation is one parsed metadata.derived_from declaration.
type derivation struct {
	rel, url, license string
}

// ReadUpstreamCreditSources reads, from the repository at root, the credits list, REUSE.toml,
// every canonical persona and skill, whether LICENSES/ holds the text of each license they
// declare, the dependency inventory, the credits exceptions of .standards.yaml and the text of
// every file the list names.
func ReadUpstreamCreditSources(ctx context.Context, root string) (UpstreamCreditSources, error) {
	credits, err := ReadCredits(ctx, root)
	if err != nil {
		return UpstreamCreditSources{}, err
	}
	reuse, _, err := contextopt.ObserveSnapshotIn(ctx, root, ReuseFile)
	if err != nil {
		return UpstreamCreditSources{}, fmt.Errorf("read %s: %w", ReuseFile, err)
	}
	assets, err := compiler.CanonicalAssets(ctx, root)
	if err != nil {
		return UpstreamCreditSources{}, err
	}
	texts, err := readLicenseTexts(ctx, root, assets)
	if err != nil {
		return UpstreamCreditSources{}, err
	}
	inventory, err := ReadCreditInventory(ctx, root)
	if err != nil {
		return UpstreamCreditSources{}, err
	}
	exceptions, err := readCreditExceptions(root)
	if err != nil {
		return UpstreamCreditSources{}, err
	}
	paths, err := readCreditPathTexts(ctx, root, credits)
	if err != nil {
		return UpstreamCreditSources{}, err
	}
	return UpstreamCreditSources{
		Credits: credits, Reuse: string(reuse), Assets: assets, LicenseTexts: texts,
		Inventory: inventory, Exceptions: exceptions, PathTexts: paths, Today: time.Now(),
	}, nil
}

// readLicenseTexts reports, for each license identifier the assets declare, whether
// LICENSES/<id>.txt exists below root. A declaration that does not parse names none here;
// CheckUpstreamCredits reports it.
func readLicenseTexts(ctx context.Context, root string, assets []compiler.AssetUpstream) (map[string]bool, error) {
	texts := map[string]bool{}
	for _, asset := range assets {
		parsed, err := parseDerivation(asset)
		if asset.DerivedFrom == "" || err != nil {
			continue
		}
		for _, id := range licenseTerms(parsed.license) {
			if _, seen := texts[id]; seen {
				continue
			}
			_, exists, err := contextopt.ObserveSnapshotIn(ctx, root, LicensesDir+"/"+id+".txt")
			if err != nil {
				return nil, fmt.Errorf("read %s/%s.txt: %w", LicensesDir, id, err)
			}
			texts[id] = exists
		}
	}
	return texts, nil
}

// readCreditExceptions returns the entries of the manifest's declared exceptions list under
// config.ExceptionRuleCredits, read and validated by config.LoadManifest. A repository without a
// manifest declares none.
func readCreditExceptions(root string) ([]config.Exception, error) {
	manifest, err := config.LoadManifest(filepath.Join(root, config.ManifestFileName))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return config.ExceptionsFor(manifest.Exceptions, config.ExceptionRuleCredits), nil
}

// readCreditPathTexts reads every path the credits list names below root and returns each
// file's text lower-cased; a path that is no file is left out.
func readCreditPathTexts(ctx context.Context, root string, credits Credits) (map[string]string, error) {
	texts := map[string]string{}
	for _, rel := range creditPaths(credits) {
		data, exists, err := contextopt.ObserveSnapshotIn(ctx, root, rel)
		if err != nil {
			return nil, fmt.Errorf("read %s, which %s names: %w", rel, AcknowledgementsList, err)
		}
		if exists {
			texts[rel] = strings.ToLower(string(data))
		}
	}
	return texts, nil
}

// creditPaths lists, sorted and once each, the paths of every entry, original and download.
func creditPaths(credits Credits) []string {
	paths := slices.Clone(credits.Originals)
	for _, entry := range credits.Entries {
		paths = append(paths, entry.Paths...)
	}
	for _, download := range credits.Downloads {
		paths = append(paths, download.Path)
	}
	slices.Sort(paths)
	return slices.Compact(paths)
}

// CheckUpstreamCredits fails when the credits list and the repository disagree, returning every
// finding joined: see the comment at the top of this file.
func CheckUpstreamCredits(sources UpstreamCreditSources) error {
	tables, err := ReuseAnnotationTables(sources.Reuse)
	if err != nil {
		return err
	}
	declared, findings := checkDerivations(sources, tables)
	findings = append(findings, checkCreditedAssets(sources.Credits.Entries, declared)...)
	findings = append(findings, checkAssetMarkers(sources.Assets, sources.Credits.Originals, sources.PathTexts)...)
	findings = append(findings, checkInventoryCredited(sources.Inventory, sources.Credits)...)
	findings = append(findings, checkEntryPaths(sources.Credits.Entries, sources.PathTexts)...)
	findings = append(findings, checkDownloadPaths(sources.Credits.Downloads, sources.PathTexts)...)
	findings = append(findings, checkCreditExceptions(sources.Credits.Entries, sources.Exceptions, sources.Today)...)
	return errors.Join(findings...)
}

// checkDerivations parses every declared upstream and returns the declarations by file, with a
// finding for each that does not parse or that no entry answers.
func checkDerivations(sources UpstreamCreditSources, tables []ReuseAnnotation) (map[string]derivation, []error) {
	declared := map[string]derivation{}
	var findings []error
	for _, asset := range sources.Assets {
		if asset.DerivedFrom == "" {
			continue
		}
		parsed, err := parseDerivation(asset)
		if err != nil {
			findings = append(findings, err)
			continue
		}
		declared[parsed.rel] = parsed
		findings = append(findings, checkDerivationCredit(parsed, sources.Credits.Entries, tables, sources.LicenseTexts))
	}
	return declared, findings
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

// checkDerivationCredit returns the finding for one declaration, or nil when an entry answers
// it: the entry with the same URL that names the declaring file.
func checkDerivationCredit(parsed derivation, entries []CreditEntry, tables []ReuseAnnotation, texts map[string]bool) error {
	index := slices.IndexFunc(entries, func(entry CreditEntry) bool {
		return entry.URL == parsed.url && slices.Contains(entry.Paths, parsed.rel)
	})
	if index < 0 {
		return fmt.Errorf("%s declares metadata.%s %s, and %s has no entry with that url naming %s among its paths",
			parsed.rel, compiler.DerivedFromKey, parsed.url, AcknowledgementsList, parsed.rel)
	}
	entry := entries[index]
	if entry.License != parsed.license {
		return fmt.Errorf("%s credits %s under %q, and the file declares %q; state the license the upstream names",
			entry.label(index), parsed.rel, entry.License, parsed.license)
	}
	switch entry.Relation {
	case relationAdapted, relationInspired:
		return nil
	case relationVendored:
		return checkCopiedLicense(parsed, tables, texts)
	}
	return fmt.Errorf("%s gives %s the relation %q; a derivation is %s",
		entry.label(index), parsed.rel, entry.Relation, strings.Join(derivationRelations, ", "))
}

// checkCopiedLicense returns the finding for a vendored upstream whose license is not carried:
// REUSE.toml must label the file with every license term, and LICENSES/ must hold each text.
func checkCopiedLicense(parsed derivation, tables []ReuseAnnotation, texts map[string]bool) error {
	var findings []error
	for _, id := range licenseTerms(parsed.license) {
		if !texts[id] {
			findings = append(findings, fmt.Errorf("%s copies upstream text under %s, and %s/%s.txt does not exist; add the license text",
				parsed.rel, id, LicensesDir, id))
		}
		labelled, err := ReuseLabels(tables, parsed.rel, id)
		if err != nil {
			findings = append(findings, fmt.Errorf("%s copies upstream text under %s: %w", parsed.rel, id, err))
		} else if !labelled {
			findings = append(findings, fmt.Errorf("%s copies upstream text under %s, and %s does not label it %s; add an override annotation with the upstream copyright after every table that covers it",
				parsed.rel, id, ReuseFile, id))
		}
	}
	return errors.Join(findings...)
}

// checkCreditedAssets returns a finding for every entry of a derivation relation that names a
// canonical persona or skill which declares no upstream, or another one than the entry links.
func checkCreditedAssets(entries []CreditEntry, declared map[string]derivation) []error {
	var findings []error
	for index, entry := range entries {
		if !slices.Contains(derivationRelations, entry.Relation) {
			continue
		}
		for _, rel := range entry.Paths {
			if !isCanonicalAsset(rel) {
				continue
			}
			parsed, ok := declared[rel]
			switch {
			case !ok:
				findings = append(findings, fmt.Errorf("%s credits %s to %s, and that file declares no metadata.%s; add \"%s (%s)\" to its front matter",
					entry.label(index), rel, entry.URL, compiler.DerivedFromKey, entry.URL, entry.License))
			case parsed.url != entry.URL:
				findings = append(findings, fmt.Errorf("%s credits %s to %s, and that file declares %s",
					entry.label(index), rel, entry.URL, parsed.url))
			}
		}
	}
	return findings
}

// checkAssetMarkers returns a finding for every canonical persona or skill that neither
// declares an upstream nor is listed under originals, for every listed one that declares an
// upstream as well, and for every original that names no canonical persona or skill.
func checkAssetMarkers(assets []compiler.AssetUpstream, originals []string, texts map[string]string) []error {
	var findings []error
	for _, asset := range assets {
		original := slices.Contains(originals, asset.Rel)
		switch {
		case asset.DerivedFrom == "" && !original:
			findings = append(findings, fmt.Errorf("%s declares no metadata.%s and %s does not list it under originals; credit its upstream or mark it original",
				asset.Rel, compiler.DerivedFromKey, AcknowledgementsList))
		case asset.DerivedFrom != "" && original:
			findings = append(findings, fmt.Errorf("%s lists %s under originals, and that file declares metadata.%s %q",
				AcknowledgementsList, asset.Rel, compiler.DerivedFromKey, asset.DerivedFrom))
		}
	}
	for _, original := range originals {
		_, exists := texts[original]
		if !exists || !slices.ContainsFunc(assets, func(asset compiler.AssetUpstream) bool { return asset.Rel == original }) {
			findings = append(findings, fmt.Errorf("%s lists %s under originals, which is no canonical persona or skill", AcknowledgementsList, original))
		}
	}
	return findings
}

// isCanonicalAsset reports whether rel names a file below the canonical persona or skill
// directory, the files CanonicalAssets reads.
func isCanonicalAsset(rel string) bool {
	return strings.HasPrefix(rel, compiler.CanonicalAgentsRel+"/") || strings.HasPrefix(rel, compiler.CanonicalSkillsRel+"/")
}

// checkInventoryCredited returns a finding for every identifier the inventory or the declared
// downloads list that no entry answers, naming each file that uses it.
func checkInventoryCredited(inventory []InventoryItem, credits Credits) []error {
	items := slices.Clone(inventory)
	for _, download := range credits.Downloads {
		items = append(items, InventoryItem{Kind: inventoryDownload, ID: download.ID, Path: download.Path})
	}
	missing := map[InventoryItem][]string{}
	var order []InventoryItem
	for _, item := range items {
		answered := slices.ContainsFunc(credits.Entries, func(entry CreditEntry) bool { return entry.answers(item) })
		if answered {
			continue
		}
		key := InventoryItem{Kind: item.Kind, ID: item.ID}
		if _, seen := missing[key]; !seen {
			order = append(order, key)
		}
		missing[key] = append(missing[key], item.Path)
	}
	findings := make([]error, 0, len(order))
	for _, key := range order {
		findings = append(findings, fmt.Errorf("%s uses %s %s, and no entry of %s names it among its packages; add an entry with its upstream and license and the package %q",
			strings.Join(missing[key], ", "), key.Kind, key.ID, AcknowledgementsList, packageKey(key)))
	}
	return findings
}

// checkEntryPaths returns a finding for every path an entry names that is no file in the
// repository or no longer names the item by any of its terms as a whole word (namesWord), and for
// every package of an entry that none of its paths names.
func checkEntryPaths(entries []CreditEntry, texts map[string]string) []error {
	var findings []error
	for index, entry := range entries {
		terms := entry.terms()
		for _, rel := range entry.Paths {
			text, exists := texts[rel]
			switch {
			case !exists:
				findings = append(findings, fmt.Errorf("%s names %s, which is not a file in the repository", entry.label(index), rel))
			case !slices.ContainsFunc(terms, func(term string) bool { return namesWord(text, term) }):
				findings = append(findings, fmt.Errorf("%s names %s, which no longer uses it: the file names none of %q", entry.label(index), rel, terms))
			}
		}
		for _, pkg := range entry.Packages {
			id := strings.ToLower(packageIdentifier(pkg))
			named := slices.ContainsFunc(entry.Paths, func(rel string) bool { return namesWord(texts[rel], id) })
			if !named {
				findings = append(findings, fmt.Errorf("%s answers package %s, which none of its paths names", entry.label(index), pkg))
			}
		}
	}
	return findings
}

// checkDownloadPaths returns a finding for every declared download whose file no longer names it.
func checkDownloadPaths(downloads []CreditDownload, texts map[string]string) []error {
	var findings []error
	for index, download := range downloads {
		if !namesWord(texts[download.Path], strings.ToLower(download.ID)) {
			findings = append(findings, fmt.Errorf("%s downloads[%d] says %s fetches %s, and that file does not name it",
				AcknowledgementsList, index, download.Path, download.ID))
		}
	}
	return findings
}

// namesWord reports whether text holds term as a whole word (namesTerm with wordByte): "go" is not
// found in "golang" nor "git" in "digit", while a package path still counts where a slash
// continues it ("github.com/a/b/v2"). Both are lower-cased.
func namesWord(text, term string) bool {
	return namesTerm(text, term, wordByte)
}

// wordByte reports whether b is an ASCII letter, digit or underscore.
func wordByte(b byte) bool {
	return b == '_' || b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}

// checkCreditExceptions returns a finding for every entry written license unknown that no
// unexpired credits exception excuses, for every expired credits exception, and for every
// unexpired one that excuses no such entry.
func checkCreditExceptions(entries []CreditEntry, exceptions []config.Exception, today time.Time) []error {
	var findings []error
	excuses := func(exception config.Exception, entry CreditEntry) bool {
		return entry.License == licenseUnknown && slices.ContainsFunc(entry.Paths, exception.Matches)
	}
	for index, entry := range entries {
		excused := slices.ContainsFunc(exceptions, func(exception config.Exception) bool {
			return !exception.Expired(today) && excuses(exception, entry)
		})
		if entry.License == licenseUnknown && !excused {
			findings = append(findings, fmt.Errorf("%s states license %s, and no unexpired exceptions entry with rule %s names one of its paths; verify the license upstream, or declare the exception in %s",
				entry.label(index), licenseUnknown, config.ExceptionRuleCredits, config.ManifestFileName))
		}
	}
	for _, exception := range exceptions {
		switch {
		case exception.Expired(today):
			findings = append(findings, fmt.Errorf("exceptions entry %s (%s) expired on %s; verify the license upstream or renew the entry",
				exception.Target(), config.ExceptionRuleCredits, exception.Expires))
		case !slices.ContainsFunc(entries, func(entry CreditEntry) bool { return excuses(exception, entry) }):
			findings = append(findings, fmt.Errorf("exceptions entry %s (%s) excuses no entry of %s that states license %s; remove the entry",
				exception.Target(), config.ExceptionRuleCredits, AcknowledgementsList, licenseUnknown))
		}
	}
	return findings
}
