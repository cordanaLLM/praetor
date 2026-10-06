// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package compiler

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// provenanceFixture writes the given canonical personas and skills, path below .agents to
// content, into a fresh repository root.
func provenanceFixture(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, content := range files {
		writeOutputFixture(t, filepath.Join(root, ".agents", filepath.FromSlash(rel)), content)
	}
	return root
}

const derivedSkill = "---\nname: shout\ndescription: \"Shout: loud\"\nmetadata:\n  derived_from: \"https://example.test/upstream (MIT)\"\n---\n\nBody.\n"

// Positive: a persona and a skill that declare metadata.derived_from are returned, personas
// first, with their slash paths and the trimmed value; files without a declaration, without
// front matter, or with CRLF line ends are read without error.
func TestCanonicalAssetUpstreamsPositive(t *testing.T) {
	root := provenanceFixture(t, map[string]string{
		"agents/helper.md":        "---\r\nname: helper\r\nmetadata:\r\n  derived_from: '  https://example.test/helper (Apache-2.0)  '\r\n---\r\nBody.\r\n",
		"agents/plain.md":         "# Plain persona without front matter\n",
		"skills/shout/SKILL.md":   derivedSkill,
		"skills/whisper/SKILL.md": "---\nname: whisper\ndescription: quiet\nmetadata:\n  owner: example\n---\n",
	})
	got, err := CanonicalAssetUpstreams(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	want := []AssetUpstream{
		{Rel: ".agents/agents/helper.md", DerivedFrom: "https://example.test/helper (Apache-2.0)"},
		{Rel: ".agents/skills/shout/SKILL.md", DerivedFrom: "https://example.test/upstream (MIT)"},
	}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("upstreams = %+v, want %+v", got, want)
	}
}

// CanonicalAssets returns every persona and skill, declaring or not, in the same order
// (positive); a malformed front matter fails it as it fails CanonicalAssetUpstreams (negative);
// and a repository without .agents holds none (boundary).
func TestCanonicalAssets(t *testing.T) {
	root := provenanceFixture(t, map[string]string{
		"agents/plain.md":         "# Plain persona without front matter\n",
		"skills/shout/SKILL.md":   derivedSkill,
		"skills/whisper/SKILL.md": "---\nname: whisper\ndescription: quiet\n---\n",
	})
	got, err := CanonicalAssets(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	want := []AssetUpstream{
		{Rel: ".agents/agents/plain.md"},
		{Rel: ".agents/skills/shout/SKILL.md", DerivedFrom: "https://example.test/upstream (MIT)"},
		{Rel: ".agents/skills/whisper/SKILL.md"},
	}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("assets = %+v, want %+v", got, want)
	}
	broken := provenanceFixture(t, map[string]string{"skills/s/SKILL.md": "---\nname: s\nderived_from: x\n---\n"})
	if _, err := CanonicalAssets(context.Background(), broken); !errors.Is(err, errTopLevelDerivedFrom) {
		t.Fatalf("top-level declaration: %v", err)
	}
	if got, err := CanonicalAssets(context.Background(), t.TempDir()); err != nil || len(got) != 0 {
		t.Fatalf("no .agents: %v, %v", got, err)
	}
}

// Negative: front matter that is not YAML, never closes, names derived_from at its top level
// or holds a non-string declaration fails, naming the file.
func TestCanonicalAssetUpstreamsNegative(t *testing.T) {
	for name, content := range map[string]string{
		"invalid YAML":      "---\nname: s\ndescription: Audit (HISS): bounded\n---\n",
		"unclosed":          "---\nname: s\nmetadata:\n  derived_from: \"https://example.test/u (MIT)\"\n",
		"top level":         "---\nname: s\nderived_from: \"https://example.test/u (MIT)\"\n---\n",
		"non-string value":  "---\nname: s\nmetadata:\n  derived_from: [a, b]\n---\n",
		"second YAML block": "---\nname: s\n...\nname: t\n---\n",
	} {
		root := provenanceFixture(t, map[string]string{"skills/s/SKILL.md": content})
		_, err := CanonicalAssetUpstreams(context.Background(), root)
		if err == nil || !strings.HasPrefix(err.Error(), ".agents/skills/s/SKILL.md: ") {
			t.Errorf("%s: err = %v, want one naming the skill", name, err)
		}
	}
	root := provenanceFixture(t, map[string]string{"skills/s/SKILL.md": "---\nname: s\nderived_from: x\n---\n"})
	if _, err := CanonicalAssetUpstreams(context.Background(), root); !errors.Is(err, errTopLevelDerivedFrom) {
		t.Fatalf("top-level declaration: %v", err)
	}
}

// Boundary: a repository without .agents declares nothing; an empty front matter block, an
// empty metadata map and a blank declaration declare nothing; a closing fence past the line
// bound leaves the block unclosed.
func TestCanonicalAssetUpstreamsBoundary(t *testing.T) {
	if got, err := CanonicalAssetUpstreams(context.Background(), t.TempDir()); err != nil || len(got) != 0 {
		t.Fatalf("no .agents: %v, %v", got, err)
	}
	root := provenanceFixture(t, map[string]string{
		"skills/a/SKILL.md": "---\n---\nBody.\n",
		"skills/b/SKILL.md": "---\nmetadata: {}\n---\n",
		"skills/c/SKILL.md": "---\nmetadata:\n  derived_from: \"  \"\n---\n",
	})
	if got, err := CanonicalAssetUpstreams(context.Background(), root); err != nil || len(got) != 0 {
		t.Fatalf("empty declarations: %v, %v", got, err)
	}
	long := "---\n" + strings.Repeat("# comment\n", maxFrontMatterLines) + "---\n"
	if _, err := AssetDerivedFrom([]byte(long)); !errors.Is(err, errUnclosedFrontMatter) {
		t.Fatalf("a closing fence past the bound: %v", err)
	}
	atBound := "---\n" + strings.Repeat("# comment\n", maxFrontMatterLines-2) + "---\n"
	if _, err := AssetDerivedFrom([]byte(atBound)); err != nil {
		t.Fatalf("a closing fence at the bound: %v", err)
	}
}

// The checkout: every canonical persona and skill front matter parses, and the caveman skill,
// the derived asset docs/credits.md credits, declares its upstream.
func TestCanonicalAssetUpstreamsCheckout(t *testing.T) {
	got, err := CanonicalAssetUpstreams(context.Background(), filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	for _, upstream := range got {
		if upstream.Rel == ".agents/skills/caveman/SKILL.md" && strings.HasPrefix(upstream.DerivedFrom, "https://github.com/JuliusBrussee/caveman ") {
			return
		}
	}
	t.Fatalf("the caveman skill declares no upstream: %+v", got)
}
