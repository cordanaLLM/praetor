package clientschema

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/strictjson"
)

// vendorDirForTests is the vendor directory seen from this package's directory.
var vendorDirForTests = VendorDir(filepath.Join("..", ".."))

func loadManifest(t *testing.T) *Manifest {
	t.Helper()
	manifest, err := LoadManifest()
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	return manifest
}

// TestVendoredFilesMatchTheirPins is the pin test: every vendored file equals its recorded digest.
func TestVendoredFilesMatchTheirPins(t *testing.T) {
	manifest := loadManifest(t)
	count := 0
	for _, source := range manifest.Sources {
		for _, file := range source.Files {
			if _, err := Read(vendorDirForTests, file); err != nil {
				t.Errorf("%s: %v", source.ID, err)
			}
			count++
		}
	}
	if count < 28 {
		t.Fatalf("manifest pins %d files, want the 28 of the six sources", count)
	}
}

// TestNoUnpinnedFileInTheVendorDirectory fails a vendored file the manifest does not list, and a
// listed file that is missing.
func TestNoUnpinnedFileInTheVendorDirectory(t *testing.T) {
	manifest := loadManifest(t)
	var listed []string
	for _, source := range manifest.Sources {
		for _, file := range source.Files {
			listed = append(listed, file.Path)
		}
	}
	slices.Sort(listed)
	embedded, err := VendoredPaths(vendorDirForTests)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(listed, embedded) {
		t.Fatalf("manifest and vendor directory disagree:\nmanifest: %v\nembedded: %v", listed, embedded)
	}
}

// TestVerifyRefusesADriftedCopy plants a one-byte change and expects the pin to refuse it,
// naming the file (rule 13: the pin test is shown failing).
func TestVerifyRefusesADriftedCopy(t *testing.T) {
	manifest := loadManifest(t)
	file := manifest.Sources[0].Files[0]
	data, err := Read(vendorDirForTests, file)
	if err != nil {
		t.Fatal(err)
	}
	drifted := append(slices.Clone(data), ' ')
	err = file.Verify(drifted)
	if !errors.Is(err, ErrDrift) || !strings.Contains(err.Error(), file.Path) {
		t.Fatalf("Verify(drifted) = %v, want ErrDrift naming %s", err, file.Path)
	}
	if err := file.Verify(data); err != nil {
		t.Fatalf("Verify(pinned) = %v", err)
	}
	if err := file.Verify(nil); !errors.Is(err, ErrDrift) {
		t.Fatalf("Verify(empty) = %v, want ErrDrift", err)
	}
}

func TestEverySourceNamesItsLicenceAndAPinKind(t *testing.T) {
	manifest := loadManifest(t)
	licences := map[string]bool{"Apache-2.0": true, "MIT": true}
	for _, source := range manifest.Sources {
		if !licences[source.License] {
			t.Errorf("%s: licence %q is not one of the reviewed upstream licences", source.ID, source.License)
		}
		if !strings.Contains(source.URL(source.Files[0]), source.Pin) && source.PinKind != PinHosted {
			t.Errorf("%s: URL %s does not carry the pin %s", source.ID, source.URL(source.Files[0]), source.Pin)
		}
	}
}

func TestParseManifestRefusals(t *testing.T) {
	good, err := os.ReadFile(filepath.Join("vendor", ManifestFile))
	if err != nil {
		t.Fatal(err)
	}
	text := string(good)
	cases := map[string]string{
		"duplicate member":  strings.Replace(text, `"version": 1,`, `"version": 1, "version": 1,`, 1),
		"unknown field":     strings.Replace(text, `"version": 1,`, `"version": 1, "extra": true,`, 1),
		"unknown pin kind":  strings.Replace(text, `"pin_kind": "tag"`, `"pin_kind": "branch"`, 1),
		"short digest":      strings.Replace(text, `"sha256": "0ad8cf34`, `"sha256": "0AD8CF34`, 1),
		"parent path":       strings.Replace(text, `"path": "claude/claude-code-settings.json"`, `"path": "../claude.json"`, 1),
		"insecure base":     strings.Replace(text, `"url_base": "https://raw`, `"url_base": "http://raw`, 1),
		"version two":       strings.Replace(text, `"version": 1,`, `"version": 2,`, 1),
		"short commit pin":  strings.Replace(text, `"pin": "ce64da2a95a2bd40740c2d608206f8a36024d30b"`, `"pin": "ce64da2"`, 1),
		"trailing document": text + "{}",
	}
	for name, mutated := range cases {
		if mutated == text && name != "trailing document" {
			t.Fatalf("%s: mutation did not change the manifest", name)
		}
		if _, err := ParseManifest([]byte(mutated)); err == nil {
			t.Errorf("%s: manifest accepted", name)
		}
	}
	if _, err := ParseManifest(good); err != nil {
		t.Fatalf("the shipped manifest is refused: %v", err)
	}
}

func TestLookups(t *testing.T) {
	manifest := loadManifest(t)
	if got := manifest.PinnedVersion("codex"); got != "rust-v0.162.0" {
		t.Errorf("PinnedVersion(codex) = %q", got)
	}
	if got := manifest.PinnedVersion("mcp"); got != "2025-11-25" {
		t.Errorf("PinnedVersion(mcp) = %q, falls back to the first source of any kind", got)
	}
	if got := manifest.PinnedVersion("copilot"); got != "" {
		t.Errorf("PinnedVersion(copilot) = %q, want empty: no upstream schema", got)
	}
	if _, ok := manifest.Source("nope"); ok {
		t.Error("Source(nope) found")
	}
	if _, err := manifest.Schema(vendorDirForTests, "nope.json"); err == nil {
		t.Error("Schema(nope.json) found")
	}
	hooks := manifest.ForClient("codex")
	if len(hooks) != 2 {
		t.Fatalf("codex has %d sources, want config and hooks", len(hooks))
	}
	if got := hooks[1].URL(hooks[1].Files[0]); !strings.HasPrefix(got, "https://raw.githubusercontent.com/openai/codex/rust-v0.162.0/codex-rs/hooks/schema/generated/") {
		t.Errorf("hook URL %s", got)
	}
}

// renovateExpressions reads renovate.json with the duplicate-refusing reader and returns the
// regular expressions of the custom managers that watch the client schema manifest.
func renovateExpressions(t *testing.T) []*regexp.Regexp {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "renovate.json"))
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Managers []struct {
			Patterns []string `json:"managerFilePatterns"`
			Matches  []string `json:"matchStrings"`
		} `json:"customManagers"`
	}
	opts := strictjson.Options{MaxBytes: 1 << 20, MaxDepth: 64, Names: strictjson.ExactNames}
	if err := strictjson.Validate(raw, opts); err != nil {
		t.Fatalf("renovate.json is not duplicate-free JSON: %v", err)
	}
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	var expressions []*regexp.Regexp
	for _, manager := range config.Managers {
		if !slices.ContainsFunc(manager.Patterns, func(p string) bool { return strings.Contains(p, "clientschema") }) {
			continue
		}
		for _, match := range manager.Matches {
			expressions = append(expressions, regexp.MustCompile(match))
		}
	}
	return expressions
}

// TestRenovateTracksEveryPin matches each source's pin lines with the regex custom managers of
// renovate.json, so a pin Renovate cannot see fails.
func TestRenovateTracksEveryPin(t *testing.T) {
	expressions := renovateExpressions(t)
	if len(expressions) != 2 {
		t.Fatalf("renovate.json has %d client schema expressions, want 2 (tag and commit)", len(expressions))
	}
	manifestText, err := os.ReadFile(filepath.Join("vendor", ManifestFile))
	if err != nil {
		t.Fatal(err)
	}
	tracked := map[string]bool{}
	for _, expression := range expressions {
		for _, match := range expression.FindAllStringSubmatch(string(manifestText), -1) {
			tracked[match[expression.SubexpIndex("depName")]] = true
		}
	}
	for _, source := range loadManifest(t).Sources {
		if !tracked[source.Repo] {
			t.Errorf("source %s (%s) matches no Renovate custom manager", source.ID, source.Repo)
		}
	}
	if len(tracked) < 5 {
		t.Errorf("Renovate sees %d distinct pins, want the 5 repositories of the manifest: %v", len(tracked), tracked)
	}
}

// A Renovate branch moves "pin" and nothing else. The manifest must then be refused offline,
// naming the source, until the refresh has recorded the new pin with its digests; the stale
// reader the refresh starts from still accepts it. Boundary: a digest pin that differs from a
// commit pin in one hex digit is refused too, and a missing digest pin fails the structure check.
func TestMovedPinIsRefusedUntilRefreshed(t *testing.T) {
	good, err := os.ReadFile(filepath.Join("vendor", ManifestFile))
	if err != nil {
		t.Fatal(err)
	}
	text := string(good)
	moved := strings.Replace(text, `"pin": "v0.63.0"`, `"pin": "v0.64.0"`, 1)
	if moved == text {
		t.Fatal("mutation did not change the manifest")
	}
	if _, err := ParseManifest([]byte(moved)); !errors.Is(err, ErrPinMoved) || !strings.Contains(err.Error(), "gemini-settings") {
		t.Fatalf("ParseManifest(moved pin) = %v, want ErrPinMoved naming gemini-settings", err)
	}
	if _, err := ParseStaleManifest([]byte(moved)); err != nil {
		t.Fatalf("ParseStaleManifest(moved pin) = %v, want the state a refresh starts from", err)
	}
	commit := strings.Replace(text, `"pin": "ce64da2a95a2bd40740c2d608206f8a36024d30b"`, `"pin": "ce64da2a95a2bd40740c2d608206f8a36024d30c"`, 1)
	if _, err := ParseManifest([]byte(commit)); !errors.Is(err, ErrPinMoved) {
		t.Fatalf("ParseManifest(commit pin off by one digit) = %v, want ErrPinMoved", err)
	}
	missing := strings.Replace(text, `"digest_pin": "v0.63.0",`, "", 1)
	if _, err := ParseStaleManifest([]byte(missing)); err == nil {
		t.Fatal("a source without digest_pin was accepted")
	}
}

// Only the manifest is embedded: the schema files are build-time and test-time inputs, so no
// release binary redistributes the upstream schemas. The test reads the source for its embed
// directives and fails a second one or a wider pattern.
func TestOnlyTheManifestIsEmbedded(t *testing.T) {
	source, err := os.ReadFile("clientschema.go")
	if err != nil {
		t.Fatal(err)
	}
	var directives []string
	for _, line := range strings.Split(string(source), "\n") {
		if strings.HasPrefix(line, "//go:embed") {
			directives = append(directives, line)
		}
	}
	if !slices.Equal(directives, []string{EmbedDirective}) {
		t.Fatalf("embed directives = %q, want only %q", directives, EmbedDirective)
	}
	if got := AssetPaths(); !slices.Equal(got, []string{"internal/clientschema/vendor/manifest.json"}) {
		t.Fatalf("AssetPaths = %v", got)
	}
	if len(manifestJSON) == 0 {
		t.Fatal("the embedded manifest is empty")
	}
}

// renovateRule is the part of a package rule of renovate.json the version test reads.
type renovateRule struct {
	Files          []string `json:"matchFileNames"`
	DepNames       []string `json:"matchDepNames"`
	Versioning     string   `json:"versioning"`
	ExtractVersion string   `json:"extractVersion"`
}

// renovateRules reads the package rules of renovate.json that name the client schema manifest
// and a repository.
func renovateRules(t *testing.T) []renovateRule {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "renovate.json"))
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Rules []renovateRule `json:"packageRules"`
	}
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	var rules []renovateRule
	for _, rule := range config.Rules {
		if slices.Contains(rule.Files, "internal/clientschema/vendor/manifest.json") && len(rule.DepNames) > 0 {
			rules = append(rules, rule)
		}
	}
	return rules
}

// Renovate validates the manifest's currentValue with the versioning of the rule before it looks
// anything up, and extractVersion only rewrites the datasource's releases, never currentValue. A
// pin that carries a prefix (rust-v0.162.0) is therefore tracked only by a versioning that
// parses the prefix itself. The test holds each repository-specific rule to that: no
// extractVersion, and where the versioning is a regex, it matches the pin and refuses a
// pre-release tag of the same shape.
func TestRenovateVersioningParsesThePinsItTracks(t *testing.T) {
	rules := renovateRules(t)
	if len(rules) == 0 {
		t.Fatal("renovate.json has no package rule for a pinned client schema repository")
	}
	manifest := loadManifest(t)
	for _, rule := range rules {
		if rule.ExtractVersion != "" {
			t.Errorf("rule for %v sets extractVersion %q, which Renovate never applies to the current value", rule.DepNames, rule.ExtractVersion)
		}
		pattern, isRegex := strings.CutPrefix(rule.Versioning, "regex:")
		if !isRegex {
			continue
		}
		expression, err := regexp.Compile(pattern)
		if err != nil {
			t.Errorf("versioning %q: %v", rule.Versioning, err)
			continue
		}
		for _, source := range manifest.Sources {
			if !slices.Contains(rule.DepNames, source.Repo) {
				continue
			}
			if !expression.MatchString(source.Pin) {
				t.Errorf("versioning %q does not parse the pin %q of %s: Renovate would skip it as an invalid value", rule.Versioning, source.Pin, source.ID)
			}
			if expression.MatchString(source.Pin + "-alpha.1") {
				t.Errorf("versioning %q accepts a pre-release of %q", rule.Versioning, source.Pin)
			}
		}
	}
}
