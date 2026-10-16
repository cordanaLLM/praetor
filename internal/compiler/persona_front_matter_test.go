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
