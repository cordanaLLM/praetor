package compiler

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
)

func skillFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, name := range []string{"hiss-audit", "repo-adopt"} {
		dir := filepath.Join(root, filepath.FromSlash(CanonicalSkillsRel), name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		body := "---\nname: " + name + "\ndescription: fixture\n---\n\nBody.\n"
		if err := os.WriteFile(filepath.Join(dir, SkillEntryName), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	manifest := filepath.Join(root, filepath.FromSlash(PluginManifestRel))
	if err := os.MkdirAll(filepath.Dir(manifest), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifest, []byte(`{"name":"praetor"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

// Negative: a skill the repository does not declare must not ship.
func TestVerifyPluginSkills_Negative_RejectsAnOrphanSkill(t *testing.T) {
	root := skillFixture(t)
	if err := compileFixture(t, root); err != nil {
		t.Fatal(err)
	}
	orphan := filepath.Join(root, filepath.FromSlash(PluginSkillsRel), "not-declared")
	if err := os.MkdirAll(orphan, 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := VerifyPluginSkills(context.Background(), root)
	if err == nil {
		t.Fatal("an undeclared skill was shipped without complaint")
	}
	if !strings.Contains(err.Error(), "not-declared") {
		t.Errorf("error does not name the orphan: %v", err)
	}
}

// Negative: a shipped copy that drifts from its declaration is reported.
func TestVerifyPluginSkills_Negative_RejectsADriftedCopy(t *testing.T) {
	root := skillFixture(t)
	if err := compileFixture(t, root); err != nil {
		t.Fatal(err)
	}
	shipped := filepath.Join(root, filepath.FromSlash(PluginSkillsRel), "hiss-audit", SkillEntryName)
	if err := os.WriteFile(shipped, []byte("drifted\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyPluginSkills(context.Background(), root); err == nil {
		t.Fatal("a drifted plugin skill verified")
	}
}

// Boundary: every skill the rendered register block names is one this repository declares
// and its plugin ships byte-identical, so a register row never points an agent at a skill
// it cannot load. The internal row names caveman; the forbidden internal-brief name of
// ADR-0010 stays undeclared.
func TestRegisterBlockSkills_Boundary_DeclaredAndProjected(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	block, err := config.RenderRegisterBlock(config.DefaultRegisterPolicy(), false)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	var names []string
	for _, match := range regexp.MustCompile("`([a-z0-9-]+)` skill").FindAllStringSubmatch(block, -1) {
		names = append(names, match[1])
	}
	if strings.Join(names, ",") != "social-text,caveman" {
		t.Fatalf("register block must name social-text and caveman, named %v:\n%s", names, block)
	}
	for _, name := range names {
		data, err := readCanonicalSkill(context.Background(), root, name)
		if err != nil {
			t.Fatalf("register block names %s, which the repository does not declare: %v", name, err)
		}
		// \r? keeps the check true on a Windows checkout, where text=auto writes CRLF.
		if !regexp.MustCompile(`(?m)^name: ` + regexp.QuoteMeta(name) + `\r?$`).Match(data) {
			t.Errorf("%s/%s/%s frontmatter must name %s", CanonicalSkillsRel, name, SkillEntryName, name)
		}
		if err := verifyProjection(context.Background(), root, SkillEntryRel(PluginSkillsRel, name), data); err != nil {
			t.Errorf("plugin must ship %s: %v", name, err)
		}
	}
	if _, err := readCanonicalSkill(context.Background(), root, "internal-brief"); err == nil {
		t.Error("internal-brief is forbidden by ADR-0010; caveman is the one internal skill")
	}
}

// praetorSkill reads the SKILL.md or LICENSE of skill name from this repository's tree, which is
// the source bundle Praetor ships.
func praetorSkillFile(t *testing.T, name, file string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(CanonicalSkillsRel), name, file))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// Positive: the skills Praetor ships name no path an adopter lacks, and each one derived from an
// upstream under a licence that travels with copies ships that licence beside it, with the
// upstream holder's copyright line.
func TestCheckShippedSkillReferences_Positive_PraetorBundleIsClean(t *testing.T) {
	holders := map[string]string{"caveman": "Copyright (c) 2026 Julius Brussee\n", "adhd-format": "Copyright (c) 2026 Ayoub Ghriss\n", "social-text": "Copyright (c) 2026 Ayoub Ghriss\n"}
	for _, name := range config.RegisterSkillBundle() {
		text := praetorSkillFile(t, name, SkillEntryName)
		if err := CheckShippedSkillReferences(name, text); err != nil {
			t.Errorf("%s: %v", name, err)
		}
		required, err := SkillRequiresLicense(text)
		if err != nil || !required {
			t.Fatalf("%s: SkillRequiresLicense = %v, %v; want true", name, required, err)
		}
		if license := string(praetorSkillFile(t, name, SkillLicenseName)); !strings.HasPrefix(license, "MIT License\n") && !strings.Contains(license, "\nMIT License\n") || !strings.Contains(license, holders[name]) {
			t.Errorf("%s LICENSE does not carry the MIT text with the line %q", name, holders[name])
		}
	}
}

// Positive: absolute URLs, anchors, shipped files, sibling skills and the listed tokens pass,
// however wrapped.
func TestCheckShippedSkillReferences_Positive_AllowedTargets(t *testing.T) {
	cases := map[string]string{
		"own licence link":      "See [licence](LICENSE).",
		"own skill link":        "See [skill](./SKILL.md).",
		"sibling skill":         "Derived from [adhd-format](../adhd-format/SKILL.md).",
		"sibling licence":       "See [licence](../adhd-format/LICENSE).",
		"absolute URL":          "Adapted from [caveman](https://github.com/JuliusBrussee/caveman).",
		"URL with a path query": "See https://example.org/a?next=docs/credits.md for it.",
		"anchor":                "See [section](#features).",
		"adopted files by name": "See `AGENTS.md` and `.standards.yaml`; edit CHANGELOG.md never.",
		"canonical skill path":  "Read `.agents/skills/caveman/SKILL.md` first.",
		"client skill licence":  "Read `.claude/skills/caveman/LICENSE`.",
		"skill directories":     "Skills live in .agents/skills/ and .claude/skills.",
		"slash between letters": "x and / or y",
		"fragment link":         "See [rules](../caveman/SKILL.md#rules).",
	}
	for name, text := range cases {
		if err := CheckShippedSkillReferences("caveman", []byte(text)); err != nil {
			t.Errorf("%s: unexpected error: %v", name, err)
		}
	}
}

// Negative (planted, Rule 13): every way of writing a repository path an adopter lacks is refused,
// wrapped in emphasis, quotes, angle brackets or brackets, glued to a label, second on a line,
// closing a sentence, without an extension, in a fence, with backslashes, or as a link target.
func TestCheckShippedSkillReferences_Negative_UnshippedPathsRefused(t *testing.T) {
	cases := map[string]string{
		"credit line":             "Credit: docs/credits.md",
		"italic":                  "Credit: *docs/credits.md*",
		"bold":                    "Credit: **docs/credits.md**",
		"underscore emphasis":     "Credit: _docs/credits.md_",
		"double quoted":           `Credit: "docs/credits.md"`,
		"single quoted":           "Credit: 'docs/credits.md'",
		"angle brackets":          "Credit: <docs/credits.md>",
		"square brackets":         "Credit: [docs/credits.md]",
		"glued to the label":      "Credit:docs/credits.md",
		"code span":               "Credit: `docs/credits.md`",
		"double backtick span":    "Credit: ``docs/credits.md``",
		"second path on a line":   "see .agents/skills/caveman/SKILL.md docs/credits.md",
		"sentence final dot":      "Run scripts/foo.sh.",
		"sentence final dots":     "Run tools/figures/README.md...",
		"extensionless":           "Build cmd/standardsctl first",
		"fence":                   "```sh\ncat docs/credits.md\n```",
		"backslashes":             `Read docs\credits.md`,
		"flag value":              "Pass --out=docs/credits.md",
		"internal package":        "Edit `internal/compiler/register.go`",
		"workingdir":              "Write .workingdir/evidence/run.txt",
		"github directory":        "Fill .github/pull_request_template.md",
		"changelog fragments":     "Add changelog.d/x.yaml",
		"parent escape":           "See ../../README.md",
		"absolute path":           "See /etc/passwd",
		"unknown sibling":         "See ../unknown-skill/SKILL.md",
		"unshipped sibling file":  "See ../adhd-format/other.md",
		"link":                    "See [credits](docs/credits.md).",
		"reference definition":    "[credits]: docs/credits.md\nSee [credits].",
		"html anchor":             `<a href="docs/credits.yaml">credits</a>`,
		"padded link":             "[x]( docs/credits.yaml )",
		"bare link target":        "See [readme](README.md).",
		"notice link":             "See [notice](NOTICE).",
		"link with a title":       `See [x](docs/credits.md "credits").`,
		"link to an own parent":   "See [x](..).",
		"url with pipe separator": "|https://x.org|docs/credits.md|",
		"html entity slash":       "See docs&#47;credits.md",
		"html entity sol":         "See docs&sol;credits.md",
		"double backslash":        `Read docs\\credits.md`,
		"indented reference":      "   [r]: docs/credits.md\nSee [r].",
		"html image":              `<img src="credits.png">`,
		"nested bracket link":     "[a [b] c](docs/credits.md)",
		// Forms an earlier round let through (the HTML tag strip and the unit suffixes).
		"html anchor unquoted":     "<a href=docs/credits.md>credits</a>",
		"html anchor with spaces":  `<a href = "docs/credits.md">credits</a>`,
		"html link element":        `<link href="docs/credits.md">`,
		"html iframe":              `<iframe src="docs/credits.html"></iframe>`,
		"html image alt then src":  `<img alt=x src=docs/a.png>`,
		"html span title":          `<span title="docs/credits.md">x</span>`,
		"unknown tag":              "Credit <see docs/credits.md>",
		"lone angle before a path": "Compare a < b and see docs/credits.md -> done",
		"open tag then next line":  "Compare a <b\ndocs/credits.md\nlater > done",
		"path ending in /s":        "See docs/credits/s",
		"path ending in /min":      "Run scripts/bench/min",
		"unit rate":                "Handles 100 req/s.",
		"entity hex slash":         "See docs&#x2F;credits.md",
		"entity decimal slash":     "See docs&#47;credits.md.",
		"entity of a dot":          "See docs/credits&#46;md",
		"split code spans":         "See `docs`/`credits.md`",
		"split code spans spaced":  "See `docs` / `credits.md` and `docs`/`credits`",
		"attribute then path":      "href=docs/credits.md",
		"markdown escape":          `Use snake\_case here.`,
		"url glued to a path":      "https://example.org|docs/credits.md",
		"mail address":             "Write lusoris@example.org.",
		"mailto destination":       "[mail]: mailto:lusoris@example.org",
		"closing tag":              "<details>x</details>",
		"fraction":                 "Ratio 1/2 of items.",
		"slash word":               "Use and/or here.",
		"ci word":                  "Runs in CI/CD pipeline.",
		"io word":                  "Network I/O operations.",
	}
	for name, text := range cases {
		if err := CheckShippedSkillReferences("caveman", []byte(text)); err == nil {
			t.Errorf("%s: %q was accepted", name, text)
		}
	}
	err := CheckShippedSkillReferences("caveman", []byte("Credit: docs/credits.md"))
	if err == nil || !strings.Contains(err.Error(), "docs/credits.md") || !strings.Contains(err.Error(), "caveman") {
		t.Fatalf("the refusal does not name the skill and the path: %v", err)
	}
}

// Boundary: the bound fails closed. A path behind more allowed code spans than the old scan limit
// is still refused; exactly the token bound passes, one more is an error naming the bound.
func TestCheckShippedSkillReferences_Boundary_FailsClosedAtTheBound(t *testing.T) {
	spans := strings.Repeat("`AGENTS.md` ", 130)
	if err := CheckShippedSkillReferences("caveman", []byte(spans+"docs/credits.md")); err == nil || !strings.Contains(err.Error(), "docs/credits.md") {
		t.Fatalf("a path behind 130 allowed spans: err = %v, want a refusal naming it", err)
	}
	atBound := strings.Repeat("a ", maxSkillReferenceTokens)
	if err := CheckShippedSkillReferences("caveman", []byte(atBound)); err != nil {
		t.Fatalf("exactly %d tokens: %v", maxSkillReferenceTokens, err)
	}
	over := atBound + "a"
	if err := CheckShippedSkillReferences("caveman", []byte(over)); err == nil || !strings.Contains(err.Error(), strconv.Itoa(maxSkillReferenceTokens)) {
		t.Fatalf("one token past the bound: err = %v, want an error naming %d", err, maxSkillReferenceTokens)
	}
}

// Positive: a copy an adopter already holds never fails compile-context or audit. The lint of
// canonical skills does not run the reference check, so a hand-edited copy naming the adopter's
// own docs/credits.md is read as it is.
func TestLintCanonicalSkillFiles_Positive_AdopterCopyNeverRefused(t *testing.T) {
	root := t.TempDir()
	text := string(praetorSkillFile(t, "caveman", SkillEntryName)) + "\nCredit: docs/credits.md\n"
	writeRegisterFixture(t, root, CanonicalSkillRel("caveman"), text)
	if err := CheckShippedSkillReferences("caveman", []byte(text)); err == nil {
		t.Fatal("the planted copy passes the source bundle check, so this test proves nothing")
	}
	if _, err := LintCanonicalSkillFiles(t.Context(), root); err != nil {
		t.Fatalf("an adopter copy naming docs/credits.md failed the canonical skill lint: %v", err)
	}
}

// Positive, negative and boundary: whether a skill needs its licence file follows the licence its
// metadata.derived_from declares, never a word elsewhere in the text. Protect by default: every
// licence requires its text except the explicit no-notice set (noNoticeLicenses).
func TestSkillRequiresLicense(t *testing.T) {
	front := func(derived string) string {
		return "---\nname: x\nmetadata:\n  derived_from: \"" + derived + "\"\n---\n\nBody.\n"
	}
	cases := []struct {
		name string
		text string
		want bool
	}{
		{"MIT upstream", front("https://example.org/a (MIT)"), true},
		{"Apache upstream", front("https://example.org/a (Apache-2.0)"), true},
		{"BSD upstream", front("https://example.org/a (BSD-3-Clause)"), true},
		{"GPL upstream", front("https://example.org/a (GPL-3.0-only)"), true},
		{"LGPL upstream", front("https://example.org/a (LGPL-2.1)"), true},
		{"MPL upstream", front("https://example.org/a (MPL-2.0)"), true},
		{"EUPL upstream", front("https://example.org/a (EUPL-1.2)"), true},
		{"CC-BY upstream", front("https://example.org/a (CC-BY-4.0)"), true},
		{"either licence of two", front("https://example.org/a (EUPL-1.2 OR MIT)"), true},
		{"0BSD no-notice", front("https://example.org/a (0BSD)"), false},
		{"CC0 no-notice", front("https://example.org/a (CC0-1.0)"), false},
		{"MIT-0 no-notice", front("https://example.org/a (MIT-0)"), false},
		{"Unlicense no-notice", front("https://example.org/a (Unlicense)"), false},
		{"no upstream", "---\nname: x\n---\n\nBody.\n", false},
		{"MIT in the body only", "---\nname: x\n---\n\nUnder MIT terms and Apache-2.0.\n", false},
		{"no front matter", "MIT", false},
	}
	for _, tc := range cases {
		got, err := SkillRequiresLicense([]byte(tc.text))
		if err != nil || got != tc.want {
			t.Errorf("%s: SkillRequiresLicense = %v, %v; want %v", tc.name, got, err, tc.want)
		}
	}
	for _, undeclared := range []string{
		"https://example.org/a",
		"https://example.org/a ()",
		"https://example.org/a (   )",
	} {
		if _, err := SkillRequiresLicense([]byte(front(undeclared))); !errors.Is(err, errSkillLicenseUndeclared) {
			t.Errorf("undeclared %q: err = %v, want errSkillLicenseUndeclared", undeclared, err)
		}
	}
	for _, unparseable := range []string{
		"https://example.org/a (MIT/Apache-2.0)",
		"https://example.org/a (MIT, Apache-2.0)",
		"https://example.org/a ((MIT))",
		"https://example.org/a (AND MIT)",
		"https://example.org/a (MIT AND)",
		"https://example.org/a (MIT; Apache-2.0)",
	} {
		if _, err := SkillRequiresLicense([]byte(front(unparseable))); !errors.Is(err, errSkillLicenseUnparseable) {
			t.Errorf("unparseable %q: err = %v, want errSkillLicenseUnparseable", unparseable, err)
		}
	}
}
