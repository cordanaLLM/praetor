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
			if _, err := Read(file); err != nil {
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
	embedded, err := EmbeddedPaths()
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
	data, err := Read(file)
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
	if _, err := manifest.Schema("nope.json"); err == nil {
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

// TestRenovateTracksEveryPin matches each source's pin lines with the regex custom managers of
// renovate.json, read with the duplicate-refusing reader, so a pin Renovate cannot see fails.
func TestRenovateTracksEveryPin(t *testing.T) {
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
