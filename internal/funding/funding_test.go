package funding

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
)

const fixtureReadme = `# Project

  [![License](https://example.invalid/license.svg)](LICENSE)
  <!-- praetor:funding-badges:start -->
  <!-- praetor:funding-badges:end -->

---

<!-- praetor:funding-support:start -->
<!-- praetor:funding-support:end -->

## Architecture
`

const fixtureMkdocs = `site_name: Fixture
extra:
  # praetor:funding-announcement:start
  # praetor:funding-announcement:end
  social:
    - icon: fontawesome/brands/github
      link: https://example.invalid/repo
      name: Repository
    # praetor:funding-social:start
    # praetor:funding-social:end
`

const fixtureSponsoring = `# Bounties

1. Browse the [bounty board](#bounty-board).

## Bounty board

<!-- praetor:funding-bounties:start -->
> No Polar.sh account is configured, so this site links no bounty board. The operator names one as ` + "`polar` in `.config/operator/funding.yaml`" + `.
<!-- praetor:funding-bounties:end -->
`

const fixtureMonetization = `# Strategy

## Community Sponsoring Channels

<!-- praetor:funding-channels:start -->
No sponsoring channel is configured, so this site links no sponsoring account. The operator lists them in ` + "`.config/operator/funding.yaml`" + `.
<!-- praetor:funding-channels:end -->
`

const fullConfig = `github: [exampleOrg]
polar: exampleOrg
ko_fi: example
open_collective: example-collective
custom: ["https://example.org/donate"]
`

func writeFixture(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readFixture(t *testing.T, root, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func fixtureRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFixture(t, root, "README.md", fixtureReadme)
	writeFixture(t, root, "mkdocs.yml", fixtureMkdocs)
	writeFixture(t, root, "docs/sponsoring.md", fixtureSponsoring)
	writeFixture(t, root, "docs/monetization.md", fixtureMonetization)
	return root
}

func mustParse(t *testing.T, body string) *Config {
	t.Helper()
	cfg, err := Parse([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestParseValidatesTheFundingDocument(t *testing.T) {
	cfg := mustParse(t, fullConfig)
	if !cfg.Configured() || len(cfg.links()) != 4 {
		t.Fatalf("full document: %+v", cfg)
	}
	for _, bad := range []string{
		"githb: [x]\n",
		"github: [\"bad account\"]\n",
		"ko_fi: \"-leading\"\n",
		"custom: [\"http://example.org\"]\n",
		"custom: [\"https://example.org/a b\"]\n",
		"github: [a, b, c, d, e]\n",
		"polar: x\nmessage: \"two\\nlines\"\n",
		"polar: x\nmessage: \"" + strings.Repeat("m", maxMessageBytes+1) + "\"\n",
	} {
		if _, err := Parse([]byte(bad)); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	// Boundary: an empty document and one with empty lists configure nothing.
	for _, empty := range []string{"", "github: []\ncustom: []\n", "polar: \"\"\n"} {
		if mustParse(t, empty).Configured() {
			t.Errorf("%q must not be configured", empty)
		}
	}
	var absent *Config
	if absent.Configured() || len(absent.links()) != 0 {
		t.Error("a nil config must configure nothing")
	}
	// The operator's own sentence replaces the default support prose; alone it publishes nothing.
	withMessage := mustParse(t, "polar: x\nmessage: Every contribution funds the verification engines.\n")
	if support := strings.Join(renderSupport(withMessage, ""), "\n"); !strings.Contains(support, "Every contribution funds") {
		t.Errorf("message not rendered:\n%s", support)
	}
	if mustParse(t, "message: Thanks.\n").Configured() {
		t.Error("a message without a channel must not be configured")
	}
}

func TestLoadReportsAnAbsentDocumentAsNotConfigured(t *testing.T) {
	root := t.TempDir()
	if _, err := Load(root, ConfigFile); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("absent document: %v", err)
	}
	writeFixture(t, root, ConfigFile, "polar: exampleOrg\n")
	cfg, err := Load(root, ConfigFile)
	if err != nil || cfg.Polar != "exampleOrg" {
		t.Fatalf("present document: %+v, %v", cfg, err)
	}
	writeFixture(t, root, ConfigFile, "unknown: true\n")
	if _, err := Load(root, ConfigFile); err == nil || errors.Is(err, ErrNotConfigured) {
		t.Fatalf("an invalid document must fail, not read as unconfigured: %v", err)
	}
}

// Positive: a configured document renders every surface, and a second run finds no drift.
func TestApplyRendersConfiguredSurfaces(t *testing.T) {
	root := fixtureRoot(t)
	result, err := Apply(t.Context(), root, mustParse(t, fullConfig), true)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Configured || len(result.Drifted) != len(surfaces) || len(result.Skipped) != 0 {
		t.Fatalf("result: %+v", result)
	}
	funding := readFixture(t, root, fundingFile)
	for _, want := range []string{"github: [exampleOrg]", "polar: exampleOrg", "ko_fi: example", "open_collective: example-collective", `custom: ["https://example.org/donate"]`} {
		if !strings.Contains(funding, want) {
			t.Errorf("FUNDING.yml lacks %q:\n%s", want, funding)
		}
	}
	readme := readFixture(t, root, "README.md")
	for _, want := range []string{"](https://github.com/sponsors/exampleOrg)", "## 💖 Support & Sponsorship", `<a href="https://opencollective.com/example-collective">`, "## Architecture"} {
		if !strings.Contains(readme, want) {
			t.Errorf("README lacks %q:\n%s", want, readme)
		}
	}
	mkdocs := readFixture(t, root, "mkdocs.yml")
	for _, want := range []string{"  announcement: >", "    - icon: fontawesome/solid/mug-hot", "      link: https://ko-fi.com/example", "link: https://example.invalid/repo"} {
		if !strings.Contains(mkdocs, want) {
			t.Errorf("mkdocs.yml lacks %q:\n%s", want, mkdocs)
		}
	}
	if board := readFixture(t, root, "docs/sponsoring.md"); !strings.Contains(board, `<a href="https://polar.sh/exampleOrg" target="_blank" rel="noopener">`) || strings.Contains(board, "No Polar.sh account") {
		t.Errorf("sponsoring page lacks the configured bounty board:\n%s", board)
	}
	if list := readFixture(t, root, "docs/monetization.md"); !strings.Contains(list, "- **Ko-fi**: [ko-fi.com/example](https://ko-fi.com/example) (One-time developer micro-donations).") {
		t.Errorf("monetization page lacks the configured channel list:\n%s", list)
	}
	again, err := Apply(t.Context(), root, mustParse(t, fullConfig), false)
	if err != nil || len(again.Drifted) != 0 {
		t.Fatalf("rendering must be idempotent: %+v, %v", again, err)
	}
}

// Negative: without configuration every surface renders nothing, so no link to an account
// the operator never configured is published.
func TestApplyWithoutConfigurationPublishesNoLink(t *testing.T) {
	root := fixtureRoot(t)
	if _, err := Apply(t.Context(), root, mustParse(t, fullConfig), true); err != nil {
		t.Fatal(err)
	}
	result, err := Apply(t.Context(), root, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if result.Configured {
		t.Fatal("nil config reported as configured")
	}
	for _, rel := range SurfacePaths() {
		content := readFixture(t, root, rel)
		for _, host := range []string{"sponsors/", "polar.sh", "ko-fi.com", "opencollective.com", "example.org/donate", "announcement: >"} {
			if strings.Contains(content, host) {
				t.Errorf("%s still names %q without configuration:\n%s", rel, host, content)
			}
		}
	}
	for rel, want := range map[string]string{"README.md": fixtureReadme, "mkdocs.yml": fixtureMkdocs, "docs/sponsoring.md": fixtureSponsoring, "docs/monetization.md": fixtureMonetization} {
		if content := readFixture(t, root, rel); content != want {
			t.Errorf("unconfigured rendering must restore %s exactly:\n%s", rel, content)
		}
	}
	if funding := readFixture(t, root, fundingFile); !strings.Contains(funding, "Not configured") {
		t.Errorf("FUNDING.yml must say it is not configured:\n%s", funding)
	}
}

// Boundary: a present document with empty lists renders like no document; a file without
// markers is skipped untouched; an absent FUNDING.yml is not created for nothing; a check
// run never writes.
func TestApplyBoundaries(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "README.md", "# Plain\n")
	result, err := Apply(t.Context(), root, mustParse(t, "github: []\n"), true)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Drifted) != 0 || len(result.Skipped) != len(surfaces) {
		t.Fatalf("empty configuration on an unmarked tree: %+v", result)
	}
	if readFixture(t, root, "README.md") != "# Plain\n" {
		t.Error("a README without markers was modified")
	}
	if _, err := os.Stat(filepath.Join(root, fundingFile)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("FUNDING.yml was created with nothing to publish: %v", err)
	}

	checked := fixtureRoot(t)
	result, err = Apply(t.Context(), checked, mustParse(t, fullConfig), false)
	if err != nil || len(result.Drifted) == 0 {
		t.Fatalf("check run must report drift: %+v, %v", result, err)
	}
	if readFixture(t, checked, "README.md") != fixtureReadme {
		t.Error("a check run wrote the README")
	}

	broken := t.TempDir()
	writeFixture(t, broken, "README.md", "<!-- praetor:funding-badges:start -->\n")
	if _, err := Apply(t.Context(), broken, nil, false); err == nil {
		t.Error("an unbalanced marker must fail the run")
	}
	var absent context.Context
	if _, err := Apply(absent, broken, nil, false); err == nil {
		t.Error("a nil context must fail")
	}
}

// The engine publishes no operator's funding accounts: this repository's own surfaces must
// be exactly the unconfigured rendering (issue #222, BUG-816). An operational fork, whose
// manifest records the public repository.source it was overlaid from, carries the operator's
// document under .config/operator/ and must match that document's rendering instead; a
// document left in an engine checkout (git-ignored there) is not published and is ignored.
func TestRepositoryFundingSurfacesMatchTheOperatorConfiguration(t *testing.T) {
	root := filepath.Join("..", "..")
	if _, err := os.Stat(filepath.Join(root, "README.md")); err != nil {
		t.Skipf("repository root not present: %v", err)
	}
	manifest, err := config.LoadManifest(filepath.Join(root, ".standards.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg *Config
	if manifest.Repository.Source != "" {
		cfg, err = Load(root, ConfigFile)
		if err != nil && !errors.Is(err, ErrNotConfigured) {
			t.Fatal(err)
		}
	}
	result, err := Apply(t.Context(), root, cfg, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Drifted) != 0 || len(result.Skipped) != 0 {
		t.Fatalf("repository funding surfaces differ from their rendering (configured=%v); rerun `praetorctl docs funding`, which in the engine must run without %s: %+v", result.Configured, ConfigFile, result)
	}
}

// Windows checks README.md out with CRLF endings (it is not pinned in .gitattributes), so the
// comparison ignores one consistent line-ending style and a rewrite keeps it.
func TestApplyKeepsOneConsistentLineEndingStyle(t *testing.T) {
	root := fixtureRoot(t)
	crlf := strings.ReplaceAll(fixtureReadme, "\n", "\r\n")
	writeFixture(t, root, "README.md", crlf)
	result, err := Apply(t.Context(), root, nil, false)
	if err != nil || len(result.Drifted) != 0 {
		t.Fatalf("a synchronized CRLF README must not drift: %+v, %v", result, err)
	}
	if _, err := Apply(t.Context(), root, mustParse(t, fullConfig), true); err != nil {
		t.Fatal(err)
	}
	rendered := readFixture(t, root, "README.md")
	if strings.Count(rendered, "\n") != strings.Count(rendered, "\r\n") || !strings.Contains(rendered, "<!-- praetor:funding-badges:end -->\r\n") {
		t.Fatalf("CRLF README rewritten with mixed endings:\n%q", rendered)
	}
	if mkdocs := readFixture(t, root, "mkdocs.yml"); strings.Contains(mkdocs, "\r") {
		t.Error("an LF file must stay LF")
	}
	writeFixture(t, root, "README.md", "# Mixed\r\n"+fixtureReadme)
	if _, err := Apply(t.Context(), root, nil, false); err == nil || !strings.Contains(err.Error(), "line endings") {
		t.Fatalf("mixed line endings must be refused: %v", err)
	}
}

// RenderFiles renders an in-memory tree the way Apply renders a directory, returns only files
// that exist after rendering, and refuses what Apply refuses.
func TestRenderFiles(t *testing.T) {
	files := map[string][]byte{"README.md": []byte(fixtureReadme), "mkdocs.yml": []byte(fixtureMkdocs), "notes.md": []byte("x")}
	rendered, err := RenderFiles(t.Context(), files, mustParse(t, fullConfig))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rendered["README.md"]), "https://polar.sh/exampleOrg") || !strings.Contains(string(rendered[fundingFile]), "polar: exampleOrg") {
		t.Fatalf("configured rendering: %q", rendered)
	}
	if _, ok := rendered["notes.md"]; ok {
		t.Error("a file no surface owns must not be returned")
	}
	unconfigured, err := RenderFiles(t.Context(), files, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := unconfigured[fundingFile]; ok || string(unconfigured["README.md"]) != fixtureReadme || len(unconfigured) != 2 {
		t.Fatalf("unconfigured rendering of an absent FUNDING.yml and absent docs pages: %q", unconfigured)
	}
	if _, err := RenderFiles(t.Context(), map[string][]byte{"README.md": make([]byte, maxSurfaceSize+1)}, nil); err == nil {
		t.Error("an oversized surface file must be refused")
	}
	var absent context.Context
	if _, err := RenderFiles(absent, files, nil); err == nil {
		t.Error("a nil context must fail")
	}
	want := []string{fundingFile, "README.md", "mkdocs.yml", "docs/sponsoring.md", "docs/monetization.md"}
	if got := SurfacePaths(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("SurfacePaths() = %v, want %v", got, want)
	}
}
