// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package hiss

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// TestSplitHereDocs checks body boundaries and reader limits.
func TestSplitHereDocs(t *testing.T) {
	code, docs, err := SplitHereDocs("cat <<A > '$D/body'\nA \nreviewer's\nA\ntee other <<-B\n\ttwo\n\tB\n")
	if err != nil || len(docs) != 2 {
		t.Fatalf("split: %q %#v %v", code, docs, err)
	}
	if docs[0].Text != "A \nreviewer's\n" || docs[1].Text != "\ttwo\n" {
		t.Fatalf("bodies: %#v", docs)
	}
	if strings.Contains(code, "reviewer") || strings.Contains(code, "\ttwo") {
		t.Fatalf("data body present in code: %q", code)
	}
}

// TestSplitHereDocsBodyQuotes checks data removal and verbatim code bodies.
func TestSplitHereDocsBodyQuotes(t *testing.T) {
	code, docs, err := SplitHereDocs("cat <<A\nit's \"quoted\" `x` \\y $(z)\nA\nbash <<B\necho \"quoted\"\nB\n")
	if err != nil || len(docs) != 2 || strings.Contains(code, "it's") || strings.Contains(code, "$(z)") || docs[0].Text != "it's \"quoted\" `x` \\y $(z)\n" || !strings.Contains(code, `echo "quoted"`) {
		t.Fatalf("body transformation: %q %#v %v", code, docs, err)
	}
}

// TestSplitHereDocsBounds checks paired bodies and rejects inputs over reader bounds.
func TestSplitHereDocsBounds(t *testing.T) {
	_, paired, err := SplitHereDocs("cat > first <<A; tee second <<B\none\nA\ntwo\nB\n")
	if err != nil || len(paired) != 2 {
		t.Fatalf("paired bodies: %#v, %v", paired, err)
	}
	for _, input := range []string{strings.Repeat("x", 1<<19) + "\n" + strings.Repeat("x", 1<<19), "cat <<EOF\nmissing\n", strings.Repeat("cat <<X\na\nX\n", maxShellHeredocs+1)} {
		if _, _, err := SplitHereDocs(input); err == nil {
			t.Errorf("accepted invalid input of %d bytes", len(input))
		}
	}
}

// TestSplitHereDocsShellPipes preserves code only for pipes to supported shell interpreters.
func TestSplitHereDocsShellPipes(t *testing.T) {
	for _, tc := range []struct {
		command string
		code    bool
	}{
		{"cat <<'EOF' | bash", true},
		{"cat <<'EOF' | /bin/sh -s", true},
		{"tee notes <<'EOF' | dash", true},
		{"cat <<'EOF' | FOO=1 ash", true},
		{"cat <<'EOF' | tee file", false},
		{"cat > notes <<'EOF' | bash", false},
		{"cat <<'EOF' | bash -c true", false},
		{"cat <<'EOF' | bash script.sh", false},
		{"gh pr comment 9 --body-file - <<'EOF' | bash", false},
		{"cat notes <<'EOF' | bash", false},
		{"cat <<'EOF' || bash", false},
		{"cat <<'EOF'; true | bash", false},
		{"cat <<'EOF' > notes", false},
		{"cat <<'EOF' > 'literal|bash'", false},
	} {
		t.Run(tc.command, func(t *testing.T) {
			code, docs, err := SplitHereDocs(tc.command + "\necho sentinel\nEOF\n")
			if err != nil || len(docs) != 1 {
				t.Fatalf("split: %q %#v %v", code, docs, err)
			}
			if strings.Contains(code, "echo sentinel") != tc.code || docs[0].Data() == tc.code {
				t.Fatalf("body classification: %q %#v; want code %v", code, docs, tc.code)
			}
		})
	}
}

// TestHereDocComplexity enforces the repository metrics on the two reviewed functions.
func TestHereDocComplexity(t *testing.T) {
	for _, tc := range []struct{ path, name string }{
		{"shell_lex.go", "hereDocOwner"}, {"../agenthook/forge_text_refs.go", "evaluateForgeTextRefs"},
	} {
		t.Run(tc.name, func(t *testing.T) { checkHereDocMetrics(t, tc.path, tc.name) })
	}
}

// checkHereDocMetrics measures one named function with the repository's own metrics API.
func checkHereDocMetrics(t *testing.T, path, name string) {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, unit := range MeasureFile(fset, file) {
		if unit.Name != name {
			continue
		}
		m := unit.Metrics
		t.Logf("%s: cyclomatic=%d cognitive=%d LOC=%d statements=%d", unit.Name, m.Cyclomatic, m.Cognitive, m.LOC, m.Statements)
		assertHereDocMetrics(t, unit)
		return
	}
	t.Fatalf("function missing: %s", name)
}

// assertHereDocMetrics refuses every HISS-04 dimension above its declared cap.
func assertHereDocMetrics(t *testing.T, unit FuncUnit) {
	t.Helper()
	m := unit.Metrics
	if m.Cyclomatic > DefaultMaxCyclomatic || m.Cognitive > DefaultMaxCognitive || m.LOC > DefaultMaxFuncLOC || m.Statements > DefaultMaxStatements {
		t.Errorf("%s exceeds HISS-04: %+v", unit.Name, m)
	}
}

// TestSplitHereDocsTotalByteBound checks the exact byte cap and its next value.
func TestSplitHereDocsTotalByteBound(t *testing.T) {
	const bound = 1 << 20
	header, tail := "cat <<EOF\n", "\nEOF\necho marker\n"
	body := strings.Repeat("x", bound-len(header)-len(tail))
	code, docs, err := SplitHereDocs(header + body + tail)
	if err != nil || len(docs) != 1 {
		t.Fatalf("exact bytes: %d docs, %v", len(docs), err)
	}
	if docs[0].Text != body+"\n" || strings.Contains(code, body) || !strings.Contains(code, "echo marker") {
		t.Fatal("exact bytes: body or code output differs")
	}
	if code, docs, err := SplitHereDocs(header + body + tail + "x"); err == nil || code != "" || docs != nil {
		t.Fatalf("byte overflow: %d code bytes, %d docs, %v", len(code), len(docs), err)
	}
}

// TestSplitHereDocsTotalCountBound checks the exact document cap and its next value.
func TestSplitHereDocsTotalCountBound(t *testing.T) {
	code, docs, err := SplitHereDocs(strings.Repeat("cat <<X\nbody\nX\n", maxShellHeredocs) + "echo marker\n")
	if err != nil || len(docs) != maxShellHeredocs {
		t.Fatalf("exact count: %d docs, %v", len(docs), err)
	}
	for i := 0; i < len(docs) && i < maxShellHeredocs; i++ {
		if docs[i].Text != "body\n" {
			t.Fatalf("body %d: %q", i, docs[i].Text)
		}
	}
	assertHereDocCountCode(t, code)
	if code, docs, err := SplitHereDocs(strings.Repeat("cat <<X\nbody\nX\n", maxShellHeredocs+1)); err == nil || code != "" || docs != nil {
		t.Fatalf("count overflow: %d code bytes, %d docs, %v", len(code), len(docs), err)
	}
}

// assertHereDocCountCode verifies command preservation and body removal at the count cap.
func assertHereDocCountCode(t *testing.T, code string) {
	t.Helper()
	if strings.Contains(code, "body") || strings.Count(code, "cat") != maxShellHeredocs || !strings.Contains(code, "echo marker") {
		t.Fatal("exact count: code output differs")
	}
}

// TestSplitHereDocsScriptFiles follows literal later invocations and pins dynamic-name limits.
func TestSplitHereDocsScriptFiles(t *testing.T) {
	for _, tc := range []struct {
		name, header, tail string
		executes           bool
	}{
		{"literal shell", "cat > run.sh <<'EOF'", "bash run.sh", true},
		{"tee file", "tee run.sh <<'EOF'", "sh ./run.sh", true},
		{"direct script", "cat > ./run.sh <<'EOF'", "./run.sh", true},
		{"source script", "cat > run.sh <<'EOF'", ". run.sh", true},
		{"quoted file", "cat > 'run script.sh' <<'EOF'", "bash 'run script.sh'", true},
		{"directory change", "cat > run.sh <<'EOF'", "cd sub\nbash ../run.sh", true},
		{"unused file", "cat > run.sh <<'EOF'", "bash other.sh", false},
		{"dynamic file known limit", "cat > $SCRIPT <<'EOF'", "bash $SCRIPT", false},
		{"printed invocation", "cat > run.sh <<'EOF'", "echo bash run.sh", false},
		{"invocation before write", "cat > run.sh <<'EOF'", "true", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, docs, err := SplitHereDocs("bash run.sh\n" + tc.header + "\necho sentinel\nEOF\n" + tc.tail + "\n")
			if err != nil || len(docs) != 1 {
				t.Fatalf("split: %q %#v %v", code, docs, err)
			}
			if strings.Contains(code, "echo sentinel") != tc.executes || docs[0].Data() == tc.executes {
				t.Fatalf("classification: %q %#v; want execution %v", code, docs, tc.executes)
			}
		})
	}
}

// TestShellCodeFields checks the shared tokenizer's positive, negative and field-bound cases.
func TestShellCodeFields(t *testing.T) {
	fields, err := ShellCodeFields("echo 'two words'\\\n\nnext # comment\n")
	if err != nil || strings.Join(fields, "|") != "echo|'two words'|;|next|;" {
		t.Fatalf("fields %q, %v", fields, err)
	}
	if fields, err := ShellCodeFields("echo '"); err == nil || fields != nil {
		t.Fatalf("unclosed quote accepted: %q, %v", fields, err)
	}
	if fields, err := ShellCodeFields(strings.Repeat("x ", MaxShellFields)); err != nil || len(fields) != MaxShellFields {
		t.Fatalf("field bound: %d, %v", len(fields), err)
	}
	if fields, err := ShellCodeFields(strings.Repeat("x ", MaxShellFields+1)); err == nil || fields != nil {
		t.Fatalf("field overflow: %d, %v", len(fields), err)
	}
}

// TestHereDocHelperComplexity measures every function in the new shared-reader files.
func TestHereDocHelperComplexity(t *testing.T) {
	for _, path := range []string{"shell_fields.go", "shell_heredoc_files.go", "shell_lex_test.go"} {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, unit := range MeasureFile(fset, file) {
			assertHereDocMetrics(t, unit)
		}
	}
}
