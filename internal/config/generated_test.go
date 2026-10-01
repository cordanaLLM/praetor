// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// generatedSection is an adopter's generated section: a marker, one declined built-in and two
// artefacts, one of them a marked block inside a hand-edited file.
const generatedSection = `version: 1
generated:
  regeneration:
    branch_prefix: "render/"
    title_type: "build(render)"
  decline:
    - "devcontainer bundle"
  artefacts:
    - name: "changelog"
      paths:
        - "CHANGELOG.md"
      command: ["python3", "scripts/changelog.py", "--write"]
      sources:
        - "changelog.d/*.yaml"
      env:
        TZ: "UTC"
      timeout: "2m"
    - name: "tool table"
      paths:
        - "docs/tools.md"
      block:
        start: "<!-- tools:start -->"
        end: "<!-- tools:end -->"
      command: ["make", "tools-table"]
      sources:
        - "tools/**"
`

// Positive (#696): the section decodes as written, survives RenderManifest, and its marker,
// timeout and environment read back; an absent section and an unset marker take the defaults.
func TestLoadManifestGeneratedPositive(t *testing.T) {
	m, err := LoadManifest(writeManifest(t, generatedSection))
	if err != nil {
		t.Fatal(err)
	}
	if m.Generated == nil || len(m.Generated.Artefacts) != 2 {
		t.Fatalf("decoded %+v", m.Generated)
	}
	changelog, table := m.Generated.Artefacts[0], m.Generated.Artefacts[1]
	if changelog.RenderTimeout() != 2*time.Minute || strings.Join(changelog.EnvList(), ",") != "TZ=UTC" || changelog.Block != nil {
		t.Fatalf("changelog = %+v", changelog)
	}
	if table.Block == nil || table.Block.Start != "<!-- tools:start -->" || table.RenderTimeout() != DefaultGeneratedTimeout {
		t.Fatalf("tool table = %+v", table)
	}
	if marker := m.Generated.Marker(); marker.BranchPrefix != "render/" || marker.TitleType != "build(render)" {
		t.Fatalf("marker = %+v", marker)
	}
	rendered, err := RenderManifest(m)
	if err != nil {
		t.Fatal(err)
	}
	again, err := LoadManifest(writeManifest(t, string(rendered)))
	if err != nil || again.Generated == nil || len(again.Generated.Artefacts) != 2 || again.Generated.Decline[0] != "devcontainer bundle" {
		t.Fatalf("generated lost in the render round trip: %+v, %v\n%s", again, err, rendered)
	}
	absent, err := LoadManifest(writeManifest(t, "version: 1\n"))
	if err != nil || absent.Generated != nil {
		t.Fatalf("an absent section must decode to none: %+v, %v", absent, err)
	}
	if marker := absent.Generated.Marker(); marker.BranchPrefix != DefaultRegenerationBranchPrefix || marker.TitleType != DefaultRegenerationTitleType {
		t.Fatalf("default marker = %+v", marker)
	}
	parsed, err := ParseManifest("base:.standards.yaml", []byte(generatedSection))
	if err != nil || len(parsed.Generated.Artefacts) != 2 {
		t.Fatalf("ParseManifest = %+v, %v", parsed, err)
	}
}

// Negative (#696): every malformed declaration is refused by the loader with its position named,
// and ParseManifest applies the same validation to bytes read from a commit.
func TestLoadManifestGeneratedNegative(t *testing.T) {
	const artefact = "  artefacts:\n    - name: \"a\"\n      paths: [\"a.txt\"]\n      command: [\"make\"]\n      sources: [\"src/**\"]\n"
	cases := map[string]string{
		"  regeneration:\n    branch_prefix: \"-x y\"\n":                                                                  "generated.regeneration.branch_prefix",
		"  regeneration:\n    title_type: \"Chore: x\"\n":                                                                 "generated.regeneration.title_type",
		"  decline: [\"\"]\n":                                                                                             "generated.decline[0] must be a non-empty string",
		"  artefacts:\n    - paths: [\"a.txt\"]\n":                                                                        "generated.artefacts[0].name must be a non-empty string",
		"  artefacts:\n    - name: \"a\"\n":                                                                               "generated.artefacts[0].paths must list at least one glob",
		"  artefacts:\n    - name: \"a\"\n      paths: [\"/a\"]\n":                                                        "generated.artefacts[0].paths[0] must be repository-relative",
		"  artefacts:\n    - name: \"a\"\n      paths: [\"a.txt\"]\n      command: [\"make\"]\n":                          "generated.artefacts[0].sources must list at least one glob",
		"  artefacts:\n    - name: \"a\"\n      paths: [\"a.txt\"]\n      sources: [\"**\"]\n":                            "generated.artefacts[0].sources[0] must name a path",
		"  artefacts:\n    - name: \"a\"\n      paths: [\"a.txt\"]\n      sources: [\"src/**\"]\n":                        "generated.artefacts[0].command must name the command",
		"  artefacts:\n    - name: \"a\"\n      paths: [\"a.txt\"]\n      sources: [\"s\"]\n      command: [\" \"]\n":     "generated.artefacts[0].command must name the command",
		"  artefacts:\n    - name: \"a\"\n      paths: [\"a.txt\"]\n      sources: [\"s\"]\n      command: [\"a\\nb\"]\n": "generated.artefacts[0].command[0] must not contain a control character",
		artefact + "      block:\n        start: \"x\"\n        end: \"x\"\n":                                             "generated.artefacts[0].block.start and generated.artefacts[0].block.end must differ",
		artefact + "      block:\n        start: \" x\"\n        end: \"y\"\n":                                            "generated.artefacts[0].block.start must be a non-empty line",
		artefact + "      block:\n        start: \"x\"\n":                                                                 "generated.artefacts[0].block.end must be a non-empty line",
		artefact + "      env:\n        \"1X\": \"v\"\n":                                                                  "generated.artefacts[0].env name \"1X\"",
		artefact + "      env:\n        X: \"a\\u0000b\"\n":                                                               "generated.artefacts[0].env.X must not contain a control character",
		artefact + "      timeout: \"soon\"\n":                                                                            "generated.artefacts[0].timeout \"soon\" is not a duration",
		artefact + "      timeout: \"0s\"\n":                                                                              "must be positive and at most 30m0s",
		artefact + "    - name: \"a\"\n      paths: [\"b\"]\n      command: [\"m\"]\n      sources: [\"s\"]\n":            `generated.artefacts[1] repeats the name "a" of generated.artefacts[0]`,
		artefact + "      cmd: [\"make\"]\n":                                                                              "field cmd not found",
	}
	for section, want := range cases {
		_, err := LoadManifest(writeManifest(t, "version: 1\ngenerated:\n"+section))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("section %q: got %v, want an error containing %q", section, err, want)
		}
	}
	if _, err := ParseManifest("base:.standards.yaml", []byte("version: 1\ngenerated:\n  decline: [\"\"]\n")); err == nil ||
		!strings.Contains(err.Error(), "base:.standards.yaml") {
		t.Fatalf("ParseManifest must validate and name its source: %v", err)
	}
}

// Boundary (#696): each count, length and duration bound admits its exact limit and refuses one
// more; a nil policy is valid.
func TestValidateGeneratedBoundary(t *testing.T) {
	artefact := func(name string) GeneratedArtefact {
		return GeneratedArtefact{Name: name, Paths: []string{"out.txt"}, Command: []string{"make"}, Sources: []string{"src/**"}}
	}
	list := func(count int, item func(int) string) []string {
		values := make([]string, count)
		for index := range values {
			values[index] = item(index)
		}
		return values
	}
	artefacts := func(count int) []GeneratedArtefact {
		values := make([]GeneratedArtefact, count)
		for index := range values {
			values[index] = artefact(fmt.Sprintf("a%d", index))
		}
		return values
	}
	glob := func(index int) string { return fmt.Sprintf("src/%d.go", index) }
	word := func(index int) string { return fmt.Sprintf("w%d", index) }
	env := func(count int) map[string]string {
		values := make(map[string]string, count)
		for index := 0; index < count; index++ {
			values[fmt.Sprintf("V%d", index)] = "x"
		}
		return values
	}
	within := []struct {
		name   string
		policy GeneratedPolicy
		want   string
	}{
		{"artefacts", GeneratedPolicy{Artefacts: artefacts(MaxGeneratedArtefacts)}, ""},
		{"artefacts+1", GeneratedPolicy{Artefacts: artefacts(MaxGeneratedArtefacts + 1)}, "generated.artefacts has 65 entries; maximum is 64"},
		{"decline", GeneratedPolicy{Decline: list(MaxGeneratedDecline, word)}, ""},
		{"decline+1", GeneratedPolicy{Decline: list(MaxGeneratedDecline+1, word)}, "generated.decline has 33 names; maximum is 32"},
		{"globs", GeneratedPolicy{Artefacts: []GeneratedArtefact{{Name: "a", Paths: list(MaxGeneratedGlobs, glob), Command: []string{"m"}, Sources: list(MaxGeneratedGlobs, glob)}}}, ""},
		{"globs+1", GeneratedPolicy{Artefacts: []GeneratedArtefact{{Name: "a", Paths: list(MaxGeneratedGlobs+1, glob), Command: []string{"m"}, Sources: []string{"s"}}}}, "paths has 33 globs; maximum is 32"},
		{"words", GeneratedPolicy{Artefacts: []GeneratedArtefact{{Name: "a", Paths: []string{"o"}, Command: list(MaxGeneratedCommandArgs, word), Sources: []string{"s"}}}}, ""},
		{"words+1", GeneratedPolicy{Artefacts: []GeneratedArtefact{{Name: "a", Paths: []string{"o"}, Command: list(MaxGeneratedCommandArgs+1, word), Sources: []string{"s"}}}}, "command has 33 words; maximum is 32"},
		{"arg bytes", GeneratedPolicy{Artefacts: []GeneratedArtefact{{Name: "a", Paths: []string{"o"}, Command: []string{strings.Repeat("x", MaxGeneratedArgBytes)}, Sources: []string{"s"}}}}, ""},
		{"arg bytes+1", GeneratedPolicy{Artefacts: []GeneratedArtefact{{Name: "a", Paths: []string{"o"}, Command: []string{strings.Repeat("x", MaxGeneratedArgBytes+1)}, Sources: []string{"s"}}}}, "exceeds 1024 bytes"},
		{"env", GeneratedPolicy{Artefacts: []GeneratedArtefact{{Name: "a", Paths: []string{"o"}, Command: []string{"m"}, Sources: []string{"s"}, Env: env(MaxGeneratedEnv)}}}, ""},
		{"env+1", GeneratedPolicy{Artefacts: []GeneratedArtefact{{Name: "a", Paths: []string{"o"}, Command: []string{"m"}, Sources: []string{"s"}, Env: env(MaxGeneratedEnv + 1)}}}, "env has 17 entries; maximum is 16"},
		{"timeout", GeneratedPolicy{Artefacts: []GeneratedArtefact{{Name: "a", Paths: []string{"o"}, Command: []string{"m"}, Sources: []string{"s"}, Timeout: "30m"}}}, ""},
		{"timeout+1", GeneratedPolicy{Artefacts: []GeneratedArtefact{{Name: "a", Paths: []string{"o"}, Command: []string{"m"}, Sources: []string{"s"}, Timeout: "30m1s"}}}, "must be positive and at most 30m0s"},
		{"marker bytes", GeneratedPolicy{Regeneration: &RegenerationMarker{BranchPrefix: strings.Repeat("r", MaxGeneratedMarkerBytes)}}, ""},
		{"marker bytes+1", GeneratedPolicy{Regeneration: &RegenerationMarker{BranchPrefix: strings.Repeat("r", MaxGeneratedMarkerBytes+1)}}, "generated.regeneration.branch_prefix"},
	}
	for _, tc := range within {
		err := ValidateGenerated(&tc.policy)
		if tc.want == "" && err != nil {
			t.Errorf("%s: exactly at the bound must pass: %v", tc.name, err)
		}
		if tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
			t.Errorf("%s: got %v, want an error containing %q", tc.name, err, tc.want)
		}
	}
	if err := ValidateGenerated(nil); err != nil {
		t.Fatalf("a nil policy: %v", err)
	}
	if got := (GeneratedArtefact{Timeout: "45m"}).RenderTimeout(); got != DefaultGeneratedTimeout {
		t.Fatalf("an out-of-range timeout must fall back to the default, got %s", got)
	}
	if got := (&GeneratedPolicy{Regeneration: &RegenerationMarker{TitleType: "ci"}}).Marker(); got.BranchPrefix != DefaultRegenerationBranchPrefix || got.TitleType != "ci" {
		t.Fatalf("a half-set marker keeps the other default: %+v", got)
	}
}
