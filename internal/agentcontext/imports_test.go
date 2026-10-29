package agentcontext

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestProjectionClientSizeCaps(t *testing.T) {
	cases := []struct {
		client, file, unit string
		size, cap          int
	}{
		{"gemini", ".gemini/GEMINI.md", "bytes", 23999, 24000},
		{"gemini", ".gemini/GEMINI.md", "bytes", 24000, 24000},
		{"gemini", ".gemini/GEMINI.md", "bytes", 24001, 24000},
		{"gemini", ".gemini/GEMINI.md", "bytes", 25000, 24000},
		{"windsurf", ".windsurfrules", "characters", 11999, 12000},
		{"windsurf", ".windsurfrules", "characters", 12000, 12000},
		{"windsurf", ".windsurfrules", "characters", 12001, 12000},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%s-%d", tc.client, tc.size), func(t *testing.T) {
			tr := NewTranspiler()
			tr.Clients = []string{tc.client}
			res, err := tr.CompileContent(strings.Repeat("x", tc.size-len(generatedHeader)))
			if tc.size > tc.cap {
				want := fmt.Sprintf("target file %s exceeds client size cap (%d %s > %d)", tc.file, tc.size, tc.unit, tc.cap)
				assertImportError(t, err, want)
				return
			}
			if err != nil || len(res.Files[0].Content) != tc.size {
				t.Fatalf("size %d: result=%v error=%v", tc.size, res, err)
			}
		})
	}
}

func TestCanonicalClientSizeCap(t *testing.T) {
	for _, size := range []int{32767, 32768, 32769} {
		tr := NewTranspiler()
		tr.Clients = []string{"codex"}
		_, err := tr.CompileContent(strings.Repeat("x", size))
		if size > 32768 {
			assertImportError(t, err, "AGENTS.md", "32769 bytes > 32768")
		} else if err != nil {
			t.Fatal(err)
		}
	}
}

func TestWindsurfCapCountsUnicodeCharacters(t *testing.T) {
	for _, size := range []int{11999, 12000, 12001} {
		tr := NewTranspiler()
		tr.Clients = []string{"windsurf"}
		res, err := tr.CompileContent(strings.Repeat("é", size-utf8.RuneCountInString(generatedHeader)))
		if size > 12000 {
			assertImportError(t, err, "12001 characters > 12000")
		} else if err != nil || utf8.RuneCountInString(res.Files[0].Content) != size {
			t.Fatalf("size %d: result=%v error=%v", size, res, err)
		}
	}
}

func TestImportsTranslatePerClientAndPreserveFences(t *testing.T) {
	tr := importTranspiler(map[string]string{"docs/policy.md": "policy"})
	source := "# Policy\n@docs/policy.md\n````md\n```\n@fenced.md\n````\n~~~\n@tilde.md\n~~~\n @indented.md\ninline @mention\n"
	res, err := tr.CompileContent(source)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range res.Files {
		want := "See docs/policy.md (read on demand)."
		if file.RelativePath == "CLAUDE.md" || file.RelativePath == ".gemini/GEMINI.md" {
			want = "@docs/policy.md"
		}
		for _, line := range []string{want, "@fenced.md", "@tilde.md", " @indented.md", "inline @mention"} {
			if !strings.Contains(file.Content, "\n"+line+"\n") {
				t.Errorf("%s missing %q", file.RelativePath, line)
			}
		}
		if strings.Contains(file.Content, "See fenced.md") || strings.Contains(file.Content, "See tilde.md") {
			t.Errorf("%s translated fenced import", file.RelativePath)
		}
	}
}

func importTranspiler(files map[string]string) *Transpiler {
	tr := NewTranspiler()
	tr.ReadImport = func(name string) (string, error) {
		text, ok := files[name]
		if !ok {
			return "", fmt.Errorf("fixture file absent: %s", name)
		}
		return text, nil
	}
	return tr
}

func assertImportError(t *testing.T, err error, wants ...string) {
	t.Helper()
	if err == nil {
		t.Fatal("planted invalid context accepted")
	}
	for _, want := range wants {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err, want)
		}
	}
}

func TestImportExpansionRefusals(t *testing.T) {
	cases := []struct {
		name, source string
		files        map[string]string
		wants        []string
	}{
		{"100KB", "@bulk.md", map[string]string{"bulk.md": strings.Repeat("x", 100000)}, []string{".gemini/GEMINI.md", "bytes > 24000", "bulk.md"}},
		{"cycle", "@x", map[string]string{"x": "@y", "y": "@./x"}, []string{"cycle", "./x"}},
		{"missing", "@absent.md", nil, []string{"missing or unreadable", "absent.md"}},
		{"outside", "@../outside.md", nil, []string{"repository-root-relative", "../outside.md"}},
		{"read-bound", "@huge.md", map[string]string{"huge.md": strings.Repeat("x", maxImportBytes+1)}, []string{"huge.md", "scan bound"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tr := importTranspiler(tc.files)
			tr.Clients = []string{"gemini"}
			_, err := tr.CompileContent(tc.source)
			assertImportError(t, err, tc.wants...)
		})
	}
}

func TestExpandedSizeReplacesImportLineAndCountsRepeatedImports(t *testing.T) {
	for _, size := range []int{23999, 24000, 24001} {
		// Two occurrences count twice. The import's own bytes are replaced.
		text := strings.Repeat("x", (size-len(generatedHeader)-1)/2)
		tr := importTranspiler(map[string]string{"x": text, "y": "@x"})
		tr.Clients = []string{"gemini"}
		suffix := strings.Repeat("\n", (size-len(generatedHeader)-1)%2)
		_, err := tr.CompileContent("@y\n@x" + suffix)
		if size > 24000 {
			assertImportError(t, err, "24001 bytes > 24000")
		} else if err != nil {
			t.Fatal(err)
		}
	}
}

func TestImportDepthCaps(t *testing.T) {
	for _, client := range []string{"claude", "gemini"} {
		for _, depth := range []int{4, 5, 6} {
			files := map[string]string{}
			for i := 1; i <= depth; i++ {
				files[fmt.Sprint(i)] = fmt.Sprintf("@%d", i+1)
			}
			files[fmt.Sprint(depth)] = "policy"
			tr := importTranspiler(files)
			tr.Clients = []string{client}
			_, err := tr.CompileContent("@1")
			if depth > 5 || client == "claude" && depth > 4 {
				assertImportError(t, err, "depth cap")
			} else if err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestImportTraversalBoundaries(t *testing.T) {
	for _, count := range []int{maxImportVisits - 1, maxImportVisits, maxImportVisits + 1} {
		tr := importTranspiler(map[string]string{"x": "policy"})
		tr.Clients = []string{"claude"}
		_, err := tr.CompileContent(strings.Repeat("@x\n", count))
		if count > maxImportVisits {
			assertImportError(t, err, "too many context imports")
		} else if err != nil {
			t.Fatal(err)
		}
	}
	tr := importTranspiler(map[string]string{"x": strings.Repeat("@y\n", maxImportVisits), "y": "policy"})
	tr.Clients = []string{"claude"}
	_, err := tr.CompileContent("@x")
	assertImportError(t, err, "y", "traversal bound")
}

func TestNonExpandingClientsNeedNoImportReader(t *testing.T) {
	tr := NewTranspiler()
	tr.Clients = []string{"cursor", "copilot", "windsurf", "codex"}
	if _, err := tr.CompileContent("@missing.md"); err != nil {
		t.Fatal(err)
	}
	tr.Clients = []string{"gemini"}
	_, err := tr.CompileContent("@missing.md")
	assertImportError(t, err, "missing.md", "no repository reader")
}
