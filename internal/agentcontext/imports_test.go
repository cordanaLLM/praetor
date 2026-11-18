package agentcontext

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

// TestProjectionClientSizeCaps bounds the projection together with the canonical text it loads.
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
			overhead := len(generatedHeader) + 1 // Import line replaced; its newline remains.
			if tc.client == "windsurf" {
				overhead = len(strings.TrimRight(generatedHeader, "\n") + "\n")
			}
			source := strings.Repeat("x", tc.size-overhead)
			res, err := tr.CompileContent(source)
			if tc.size > tc.cap {
				want := fmt.Sprintf("target file %s exceeds client size cap (%d %s > %d)", tc.file, tc.size, tc.unit, tc.cap)
				assertImportError(t, err, want)
				return
			}
			if err != nil {
				t.Fatalf("size %d: result=%v error=%v", tc.size, res, err)
			}
		})
	}
}

// TestCanonicalClientSizeCap includes the Codex projection in the loaded document cap.
func TestCanonicalClientSizeCap(t *testing.T) {
	for _, size := range []int{32767, 32768, 32769} {
		tr := NewTranspiler()
		tr.Clients = []string{"codex"}
		_, err := tr.CompileContent(strings.Repeat("x", size-len(strings.TrimRight(generatedHeader, "\n")+"\n")))
		if size > 32768 {
			assertImportError(t, err, "AGENTS.md", "32769 bytes > 32768")
		} else if err != nil {
			t.Fatal(err)
		}
	}
}

// TestWindsurfCapCountsUnicodeCharacters counts loaded canonical Unicode as characters.
func TestWindsurfCapCountsUnicodeCharacters(t *testing.T) {
	for _, size := range []int{11999, 12000, 12001} {
		tr := NewTranspiler()
		tr.Clients = []string{"windsurf"}
		source := strings.Repeat("é", size-utf8.RuneCountInString(strings.TrimRight(generatedHeader, "\n")+"\n"))
		res, err := tr.CompileContent(source)
		if size > 12000 {
			assertImportError(t, err, "12001 characters > 12000")
		} else if err != nil || utf8.RuneCountInString(res.Files[0].Content+source) != size {
			t.Fatalf("size %d: result=%v error=%v", size, res, err)
		}
	}
}

// TestImportsTranslatePerClientAndPreserveFences relocates client-only imports after validation.
func TestImportsTranslatePerClientAndPreserveFences(t *testing.T) {
	tr := importTranspiler(map[string]string{"docs/policy.md": "policy"})
	source := "@docs/policy.md\n````md\n```\n@fenced.md\n````\n~~~\n@tilde.md\n~~~\n @indented.md\ninline @mention\n"
	res, err := tr.CompileContent(vendorSections(source))
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range res.Files {
		want := "See docs/policy.md (read on demand)."
		switch file.RelativePath {
		case "CLAUDE.md":
			want = "@docs/policy.md"
		case ".gemini/GEMINI.md":
			want = "@../docs/policy.md"
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

// TestExpandedSizeReplacesImportLineAndCountsRepeatedImports counts the canonical pointer hop.
func TestExpandedSizeReplacesImportLineAndCountsRepeatedImports(t *testing.T) {
	for _, size := range []int{23999, 24000, 24001} {
		// Two occurrences count twice. The import's own bytes are replaced.
		text := strings.Repeat("x", (size-len(generatedHeader)-2)/2)
		tr := importTranspiler(map[string]string{"x": text, "y": "@x"})
		tr.Clients = []string{"gemini"}
		suffix := strings.Repeat("\n", (size-len(generatedHeader)-2)%2)
		_, err := tr.CompileContent("@y\n@x" + suffix)
		if size > 24000 {
			assertImportError(t, err, "24001 bytes > 24000")
		} else if err != nil {
			t.Fatal(err)
		}
	}
}

// TestImportDepthCaps includes the projection-to-canonical hop in the client limit.
func TestImportDepthCaps(t *testing.T) {
	for _, client := range []string{"claude", "gemini"} {
		for _, depth := range []int{3, 4, 5} {
			files := map[string]string{}
			for i := 1; i <= depth; i++ {
				files[fmt.Sprint(i)] = fmt.Sprintf("@%d", i+1)
			}
			files[fmt.Sprint(depth)] = "policy"
			tr := importTranspiler(files)
			tr.Clients = []string{client}
			_, err := tr.CompileContent("@1")
			if depth+1 > 5 || client == "claude" && depth+1 > 4 {
				assertImportError(t, err, "depth cap")
			} else if err != nil {
				t.Fatal(err)
			}
		}
	}
}

// TestImportTraversalBoundaries counts the canonical pointer against traversal admission.
func TestImportTraversalBoundaries(t *testing.T) {
	for _, count := range []int{maxImportVisits - 2, maxImportVisits - 1, maxImportVisits, maxImportVisits + 1} {
		tr := importTranspiler(map[string]string{"x": "policy"})
		tr.Clients = []string{"claude"}
		_, err := tr.CompileContent(strings.Repeat("@x\n", count))
		if count > maxImportVisits {
			assertImportError(t, err, "too many context imports")
		} else if count == maxImportVisits {
			assertImportError(t, err, "traversal bound")
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

// TestProjectionLoadedLineBudget applies the budget to every client's complete loaded context.
func TestProjectionLoadedLineBudget(t *testing.T) {
	for _, target := range vendorTargets {
		t.Run(target.client, func(t *testing.T) {
			frame := strings.TrimRight(target.prefix+testHeader+testReferenceFor(target.path), "\n") + "\n"
			overhead := countLines(frame) + 1
			if target.importDepth > 0 {
				overhead = countLines(frame)
			}
			for _, extra := range []int{-1, 0, 1} {
				source := strings.Repeat("rule\n", MaxLineBudget-overhead+extra)
				tr := NewTranspiler()
				tr.Clients = []string{target.client}
				_, err := tr.CompileContent(source)
				if extra > 0 {
					assertImportError(t, err, target.path, "max line budget (301 > 300)")
				} else if err != nil {
					t.Fatalf("loaded lines %d: %v", MaxLineBudget+extra, err)
				}
			}
		})
	}
}

// TestImportExpansionLineBudget counts nested and repeated imports against the loaded line budget.
func TestImportExpansionLineBudget(t *testing.T) {
	for _, client := range []string{"claude", "gemini"} {
		t.Run(client, func(t *testing.T) {
			tr := importTranspiler(map[string]string{"bulk": strings.Repeat("rule\n", 150), "nested": "@bulk"})
			tr.Clients = []string{client}
			_, err := tr.CompileContent("@nested\n@bulk")
			assertImportError(t, err, "max line budget", "> 300")
		})
	}
}

// TestCanonicalSnapshotCapsCountCRLF preserves the original canonical snapshot for size caps.
func TestCanonicalSnapshotCapsCountCRLF(t *testing.T) {
	for _, tc := range []struct {
		client, file  string
		cap, overhead int
	}{
		{"codex", "AGENTS.md", 32768, len(strings.TrimRight(testHeader, "\n") + "\n")},
		{"windsurf", ".windsurfrules", 12000, len(strings.TrimRight(testHeader, "\n") + "\n")},
		{"gemini", ".gemini/GEMINI.md", 24000, len(testHeader) + 1},
	} {
		t.Run(tc.client, func(t *testing.T) {
			for _, extra := range []int{0, 1} {
				tr := NewTranspiler()
				tr.Clients = []string{tc.client}
				source := strings.Repeat("x", tc.cap-tc.overhead-2+extra) + "\r\n"
				_, err := tr.CompileContent(source)
				if extra > 0 {
					assertImportError(t, err, tc.file, "client size cap", fmt.Sprintf("%d", tc.cap+1))
				} else if err != nil {
					t.Fatalf("exact snapshot cap rejected: %v", err)
				}
			}
		})
	}
}
