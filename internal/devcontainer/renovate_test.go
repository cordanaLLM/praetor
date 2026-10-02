// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package devcontainer

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// Renovate's default managerFilePatterns for the two managers that read container images
// from files this repository holds, as renovatebot/renovate declares them in
// lib/modules/manager/dockerfile/index.ts and lib/modules/manager/devcontainer/index.ts.
var renovateImageManagerPatterns = []string{
	`(^|/|\.)([Dd]ocker|[Cc]ontainer)file$`,
	`(^|/)([Dd]ocker|[Cc]ontainer)file[^/]*$`,
	`^.devcontainer/devcontainer.json$`,
	`^.devcontainer.json$`,
	`^.devcontainer/[^/]+/devcontainer.json$`,
}

// generatedBundleFiles are the bundle files a Renovate manager reads, spelled out; the source
// parts are base64 text no manager parses. UpdateBotBundleFiles must name exactly these.
var generatedBundleFiles = []string{".devcontainer/devcontainer.json", ".devcontainer/Dockerfile.praetor"}

type renovateCustomManager struct {
	CustomType          string   `json:"customType"`
	ManagerFilePatterns []string `json:"managerFilePatterns"`
	MatchStrings        []string `json:"matchStrings"`
	DatasourceTemplate  string   `json:"datasourceTemplate"`
	VersioningTemplate  string   `json:"versioningTemplate"`
}

type renovateRules struct {
	CustomManagers []renovateCustomManager     `json:"customManagers"`
	PackageRules   []map[string]jsontext.Value `json:"packageRules"`
	// AutoReplaceGlobalMatch is nil while the configuration keeps Renovate's default, true.
	AutoReplaceGlobalMatch *bool `json:"autoReplaceGlobalMatch"`
}

// renovatePackageRule is the typed part of one packageRules entry the CLI pin test reads.
type renovatePackageRule struct {
	MatchManagers   []string `json:"matchManagers"`
	MatchFileNames  []string `json:"matchFileNames"`
	MatchDepNames   []string `json:"matchDepNames"`
	AllowedVersions string   `json:"allowedVersions"`
	RangeStrategy   string   `json:"rangeStrategy"`
}

// typedPackageRules decodes every package rule into renovatePackageRule, so the tests of this
// package read renovate.json through one type (readRenovate) and one decode.
func (r renovateRules) typedPackageRules(t *testing.T) []renovatePackageRule {
	t.Helper()
	rules := make([]renovatePackageRule, 0, len(r.PackageRules))
	for _, raw := range r.PackageRules {
		data, err := json.Marshal(raw)
		if err != nil {
			t.Fatal(err)
		}
		var rule renovatePackageRule
		if err := json.Unmarshal(data, &rule); err != nil {
			t.Fatal(err)
		}
		rules = append(rules, rule)
	}
	return rules
}

func readRenovate(t *testing.T) renovateRules {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "renovate.json"))
	if err != nil {
		t.Fatal(err)
	}
	var rules renovateRules
	if err := json.Unmarshal(data, &rules); err != nil {
		t.Fatal(err)
	}
	return rules
}

// renovatePatternMatches reports whether one /regex/ literal, as Renovate spells a
// managerFilePatterns entry, matches rel.
func renovatePatternMatches(t *testing.T, pattern, rel string) bool {
	t.Helper()
	expression, opened := strings.CutPrefix(pattern, "/")
	expression, closed := strings.CutSuffix(expression, "/")
	if !opened || !closed {
		t.Fatalf("file pattern %q is not a /regex/ literal", pattern)
	}
	return regexp.MustCompile(expression).MatchString(rel)
}

// readsFile returns the custom managers whose file patterns match rel.
func (r renovateRules) readsFile(t *testing.T, rel string) []renovateCustomManager {
	var managers []renovateCustomManager
	for _, manager := range r.CustomManagers {
		if slices.ContainsFunc(manager.ManagerFilePatterns, func(pattern string) bool { return renovatePatternMatches(t, pattern, rel) }) {
			managers = append(managers, manager)
		}
	}
	return managers
}

// disables reports whether a packageRule turns every update of rel off: enabled false over an
// exact matchFileNames entry, with no other member that could narrow it, the shape adoption
// counts as covering a managed path (internal/adopt/renovate.go).
func (r renovateRules) disables(rel string) bool {
	for _, rule := range r.PackageRules {
		var files []string
		if json.Unmarshal(rule["matchFileNames"], &files) != nil || !slices.Contains(files, rel) || string(rule["enabled"]) != "false" {
			continue
		}
		narrowed := false
		for member := range rule {
			narrowed = narrowed || !slices.Contains([]string{"description", "matchFileNames", "enabled"}, member)
		}
		if !narrowed {
			return true
		}
	}
	return false
}

// groups returns the groupName and matchPackageNames of the packageRule whose matchFileNames
// lists every file.
func (r renovateRules) groups(files ...string) (string, []string) {
	for _, rule := range r.PackageRules {
		var listed, packages []string
		var group string
		if json.Unmarshal(rule["matchFileNames"], &listed) != nil || json.Unmarshal(rule["groupName"], &group) != nil {
			continue
		}
		if !slices.ContainsFunc(files, func(file string) bool { return !slices.Contains(listed, file) }) {
			if raw, narrowed := rule["matchPackageNames"]; narrowed && json.Unmarshal(raw, &packages) != nil {
				return group, nil
			}
			return group, packages
		}
	}
	return "", nil
}

// reviewedPinManager returns the one custom manager that reads ReviewedPinsFile and requires it
// to read with exactly ReviewedPinPattern, the expression devcontainer bump uses, through the
// docker datasource and versioning, under Renovate's default autoReplaceGlobalMatch.
func (r renovateRules) reviewedPinManager(t *testing.T) renovateCustomManager {
	t.Helper()
	managers := r.readsFile(t, ReviewedPinsFile)
	if len(managers) != 1 {
		t.Fatalf("%d custom managers read %s, want 1", len(managers), ReviewedPinsFile)
	}
	manager := managers[0]
	if manager.CustomType != "regex" || !slices.Equal(manager.MatchStrings, []string{ReviewedPinPattern}) ||
		manager.DatasourceTemplate != "docker" || manager.VersioningTemplate != "docker" {
		t.Fatalf("reviewed-pin custom manager %+v", manager)
	}
	if r.AutoReplaceGlobalMatch != nil && !*r.AutoReplaceGlobalMatch {
		t.Fatal("renovate.json sets autoReplaceGlobalMatch false: an update would move the comment and leave the constant")
	}
	return manager
}

// Positive: one custom manager reads bootstrap.go with exactly ReviewedPinPattern. Applied to
// the committed file, it yields each reviewed default under the tag and digest of its pin
// (assertRenovateReadsThePins says why the configuration must keep autoReplaceGlobalMatch),
// and one group moves the pins with the FROM line of the development Dockerfile and nothing
// else that Dockerfile holds. The expected references are the pins' own, never a spelled tag:
// Renovate proposes tag updates into that group, and the tree must pass after one.
func TestRenovateReadsTheReviewedPinsByTag(t *testing.T) {
	rules := readRenovate(t)
	pins := committedPins(t)
	committedTree().assertRenovateReadsThePins(t, rules.reviewedPinManager(t))
	group, packages := rules.groups(ReviewedPinsFile, DevImageDockerfile)
	if group == "" {
		t.Fatalf("no packageRule groups %s with %s", ReviewedPinsFile, DevImageDockerfile)
	}
	// The development Dockerfile spells the builder in Docker Hub's short form.
	builder := pins["builder"].repository
	want := []string{builder, pins["base"].repository, strings.TrimPrefix(builder, "docker.io/library/")}
	slices.Sort(want)
	slices.Sort(packages)
	if !slices.Equal(packages, want) {
		t.Fatalf("group %q matches packages %v, want the reviewed images only %v", group, packages, want)
	}
}

// Negative: Renovate never edits the generated bundle. A packageRule disables every bundle file
// a manager reads, and no custom manager reads one.
func TestRenovateNeverReadsTheGeneratedBundle(t *testing.T) {
	rules := readRenovate(t)
	if got := UpdateBotBundleFiles(".devcontainer/devcontainer.json"); !slices.Equal(got, generatedBundleFiles) {
		t.Fatalf("UpdateBotBundleFiles = %v, want %v", got, generatedBundleFiles)
	}
	for _, rel := range generatedBundleFiles {
		if !rules.disables(rel) {
			t.Errorf("no packageRule disables Renovate for %s", rel)
		}
		if managers := rules.readsFile(t, rel); len(managers) != 0 {
			t.Errorf("custom managers %+v read the generated %s", managers, rel)
		}
	}
}

// Positive: feature updates move the catalog that selects the features, since devcontainer.json
// is generated from it. Every feature reference of every archetype and facet is read with its
// major version, and the node feature's version option with the Node.js datasource.
func TestRenovateReadsDevContainerFeaturesFromTheCatalog(t *testing.T) {
	rules := readRenovate(t)
	catalog, err := filepath.Glob(filepath.Join("..", "..", ".config", "archetypes", "*.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	facets, err := filepath.Glob(filepath.Join("..", "..", ".config", "archetypes", "facets", "*.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	features, nodeVersions := 0, 0
	for _, path := range append(catalog, facets...) {
		rel := strings.TrimPrefix(filepath.ToSlash(path), "../../")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		read := map[string]int{}
		for _, manager := range rules.readsFile(t, rel) {
			for _, pattern := range manager.MatchStrings {
				read[manager.DatasourceTemplate] += len(regexp.MustCompile(pattern).FindAllString(string(data), -1))
			}
		}
		if want := strings.Count(string(data), "\"ghcr.io/devcontainers/features/"); read["docker"] != want {
			t.Errorf("Renovate reads %d of the %d feature references in %s", read["docker"], want, rel)
		}
		features, nodeVersions = features+read["docker"], nodeVersions+read["node-version"]
	}
	if features == 0 || nodeVersions != 1 {
		t.Fatalf("catalog scan read %d features and %d node versions; it has stopped matching this tree", features, nodeVersions)
	}
}

// taglessDigests returns every image reference in content that pins a digest without a tag:
// Renovate looks such a reference up as latest (#700, #703).
func taglessDigests(content string) []string {
	var tagless []string
	for _, reference := range regexp.MustCompile(`[a-z0-9][a-z0-9./_:-]*@sha256:[a-f0-9]{64}`).FindAllString(content, -1) {
		name, _, _ := strings.Cut(reference, "@")
		if slash := strings.LastIndex(name, "/"); !strings.Contains(name[slash+1:], ":") {
			tagless = append(tagless, reference)
		}
	}
	return tagless
}

// Boundary: a tagless reference is never what Renovate is pointed at. Every repository file
// Renovate's dockerfile or devcontainer manager reads by default, or a custom manager reads,
// and no packageRule disables, pins each digest under a tag; the digest-only defaults appear
// only in bootstrap.go, where the custom manager reads the tagged line above them.
func TestRenovateNeverReadsATaglessReference(t *testing.T) {
	rules := readRenovate(t)
	listing, err := runSourceGit(t.Context(), filepath.Join("..", ".."), "ls-files", "-z")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, rel := range strings.Split(listing, "\x00") {
		read := slices.ContainsFunc(renovateImageManagerPatterns, func(pattern string) bool { return regexp.MustCompile(pattern).MatchString(rel) })
		if rel == "" || rel == ReviewedPinsFile || rules.disables(rel) || (!read && len(rules.readsFile(t, rel)) == 0) {
			continue
		}
		data, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		checked++
		if tagless := taglessDigests(string(data)); len(tagless) != 0 {
			t.Errorf("Renovate reads %s, which pins %v without a tag", rel, tagless)
		}
	}
	if checked == 0 {
		t.Fatal("no file Renovate reads was checked; the scan has stopped matching this tree")
	}
}

// The scan's own fixtures: digest-only references are reported, tagged ones, a registry port
// and a tag-only reference are not.
func TestTaglessDigestsNamesOnlyDigestOnlyReferences(t *testing.T) {
	digest := fixtureDigest(1)
	content := "FROM golang:1.27-alpine@" + digest + "\nFROM registry.test:5000/team/base:1@" + digest +
		"\nFROM golang:1.27\nimage: docker.io/library/golang@" + digest + "\nFROM registry.test:5000/team/base@" + digest + "\n"
	want := []string{"docker.io/library/golang@" + digest, "registry.test:5000/team/base@" + digest}
	if got := taglessDigests(content); !slices.Equal(got, want) {
		t.Fatalf("taglessDigests = %v, want %v", got, want)
	}
	if got := taglessDigests(""); len(got) != 0 {
		t.Fatalf("empty content reported %v", got)
	}
}
