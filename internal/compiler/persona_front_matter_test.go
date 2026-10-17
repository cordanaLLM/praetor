// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package compiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// CheckPersonaFrontMatter (#1280). Positive: a persona declaring name and description, with
// other keys and CRLF line endings, has no problem. Negative: a persona that does not parse is
// named with the decoder's error. Boundary: a duplicate key is refused by the strict reader,
// and the problems keep the order of the names given.
func TestCheckPersonaFrontMatter(t *testing.T) {
	root := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		path := filepath.Join(root, filepath.FromSlash(CanonicalAgentsRel), name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("ok.md", "---\r\nname: ok\r\ndescription: \"Fine.\"\r\nmodel: flash\r\nmainAgent: true\r\n---\r\n\r\n# ok\r\n")
	write("dup.md", "---\nname: a\nname: b\ndescription: d\n---\n")
	write("tab.md", "---\nname: t\n\tdescription: d\n---\n")
	problems, err := CheckPersonaFrontMatter(t.Context(), root, []string{"tab.md", "ok.md", "dup.md"})
	if err != nil {
		t.Fatalf("CheckPersonaFrontMatter: %v", err)
	}
	if len(problems) != 2 || problems[0].Rel != ".agents/agents/tab.md" || problems[1].Rel != ".agents/agents/dup.md" {
		t.Fatalf("problems = %+v, want tab.md then dup.md", problems)
	}
	for _, problem := range problems {
		if !strings.HasPrefix(problem.Problem, "front matter is not valid YAML") {
			t.Errorf("%s: problem %q, want a YAML decode error", problem.Rel, problem.Problem)
		}
	}
	if _, err := CheckPersonaFrontMatter(t.Context(), root, []string{"absent.md"}); err == nil {
		t.Fatal("a persona that cannot be read was not an error")
	}
}

// Positive (#1426): the tier goes in as the last front matter line, in the text's own line
// endings, and nothing else changes; the result declares the tier and lacks nothing.
func TestAddPersonaModelTier_Positive_LastFrontMatterLine(t *testing.T) {
	text := "---\nname: p\ndescription: 'Persona.'\nmainAgent: true\n---\n\n# p\n\n"
	want := "---\nname: p\ndescription: 'Persona.'\nmainAgent: true\nmodel: pro\n---\n\n# p\n\n"
	for style, pair := range map[string][2]string{
		"lf":   {text, want},
		"crlf": {strings.ReplaceAll(text, "\n", "\r\n"), strings.ReplaceAll(want, "\n", "\r\n")},
	} {
		if !PersonaLacksModelTier([]byte(pair[0])) {
			t.Fatalf("%s: a persona without model is not reported", style)
		}
		got, ok := AddPersonaModelTier([]byte(pair[0]), PersonaModelPro)
		if !ok || string(got) != pair[1] {
			t.Fatalf("%s: AddPersonaModelTier = %q, %v; want %q", style, got, ok, pair[1])
		}
		if tier, err := PersonaModelTier(got); err != nil || tier != PersonaModelPro || PersonaLacksModelTier(got) {
			t.Fatalf("%s: the result declares %q (%v)", style, tier, err)
		}
	}
}

// Negative: a text without front matter, with one that never closes, or with mixed line endings
// has no place for the line that keeps the rest intact, so nothing is returned; a persona that
// declares any model value, an invalid one included, or lacks another key, is not reported as
// lacking only its tier.
func TestAddPersonaModelTier_Negative_NoPlaceOrNotOnlyTheTier(t *testing.T) {
	for name, text := range map[string]string{
		"no front matter": "# p\n",
		"unclosed":        "---\nname: p\ndescription: d\n",
		"mixed endings":   "---\r\nname: p\ndescription: d\r\n---\n",
	} {
		if got, ok := AddPersonaModelTier([]byte(text), PersonaModelPro); ok || got != nil {
			t.Errorf("%s: AddPersonaModelTier = %q, %v; want nothing", name, got, ok)
		}
	}
	for name, text := range map[string]string{
		"invalid tier":        "---\nname: p\ndescription: d\nmodel: opus\n---\n",
		"empty tier":          "---\nname: p\ndescription: d\nmodel: \"\"\n---\n",
		"missing description": "---\nname: p\n---\n",
		"no front matter":     "# p\n",
	} {
		if PersonaLacksModelTier([]byte(text)) {
			t.Errorf("%s: reported as lacking only its model tier", name)
		}
	}
}

// Boundary: PersonaModelTier reads each allowed tier and refuses a template without one.
func TestPersonaModelTier_Boundary_AllowedTiersOnly(t *testing.T) {
	for _, tier := range []string{PersonaModelFlash, PersonaModelPro} {
		if got, err := PersonaModelTier([]byte("---\nname: p\ndescription: d\nmodel: " + tier + "\n---\n")); err != nil || got != tier {
			t.Fatalf("tier %s: got %q, %v", tier, got, err)
		}
	}
	if _, err := PersonaModelTier([]byte("---\nname: p\ndescription: d\n---\n")); err == nil {
		t.Fatal("a persona without a tier returned one")
	}
}
