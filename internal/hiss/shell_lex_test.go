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
		{"cat <<'EOF' | sh", true},
		{"cat <<'EOF' | sudo bash", true},
		{"cat <<'EOF' | /bin/sh -s", true},
		{"tee notes <<'EOF' | dash", true},
		{"cat <<'EOF' | FOO=1 ash", true},
		{"cat <<'EOF' | tee file", false},
		{"cat <<'EOF' | tee -a -i file", false},
		{"cat <<'EOF' | sudo tee file", false},
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
	for _, path := range []string{"shell_fields.go", "shell_heredoc_files.go", "shell_heredoc_owner.go", "shell_heredoc_expansions.go", "shell_lex_test.go"} {
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

// TestSplitHereDocsRound3Owners pins proven stdin ownership and redirect order.
func TestSplitHereDocsRound3Owners(t *testing.T) {
	for _, tc := range []struct {
		name, header string
		want         bool
	}{
		{"T6 command string", "bash -c true <<'EOF'", false},
		{"T6 bare shell", "bash <<'EOF'", true},
		{"T6 stdin shell", "bash -s <<'EOF'", true},
		{"T6 script operand", "bash script.sh <<'EOF'", false},
		{"T8 trailing comment", "cat <<'EOF' | bash # note", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, _, err := SplitHereDocs(tc.header + "\necho sentinel\nEOF\n")
			if err != nil || strings.Contains(code, "echo sentinel") != tc.want {
				t.Fatalf("code %q, error %v; want body %v", code, err, tc.want)
			}
		})
	}
	for _, header := range []string{"cat <<'A' <<'B' | bash", "cat <<'B' <<'A' | bash"} {
		t.Run("T7 "+header, func(t *testing.T) {
			first, last := "A", "B"
			if strings.Contains(header, "<<'B' <<'A'") {
				first, last = last, first
			}
			code, _, err := SplitHereDocs(header + "\necho discarded\n" + first + "\necho effective\n" + last + "\n")
			if err != nil || strings.Contains(code, "echo discarded") || !strings.Contains(code, "echo effective") {
				t.Fatalf("redirect order: code %q, error %v", code, err)
			}
		})
	}
}

// TestSplitHereDocsRound3Files pins literal paths and directory ownership.
func TestSplitHereDocsRound3Files(t *testing.T) {
	for _, tc := range []struct {
		name, header, tail string
		want               bool
	}{
		{"T3 quoted plus", "cat > 'build+sbom.sh' <<'EOF'", "bash 'build+sbom.sh'", true},
		{"T3 expansion", "cat > \"$SCRIPT\" <<'EOF'", "bash \"$SCRIPT\"", false},
		{"T4 pipeline cd", "cat > run.sh <<'EOF'", "cd sub | cat\nbash run.sh", true},
		{"T4 standalone cd", "cat > run.sh <<'EOF'", "cd sub\nbash ../run.sh", true},
		{"T4 subshell cd", "cat > run.sh <<'EOF'", "(cd sub)\nbash run.sh", true},
		{"T4 background cd", "cat > run.sh <<'EOF'", "cd sub &\nbash run.sh", true},
		{"T4 moved path", "cat > run.sh <<'EOF'", "cd sub\nbash run.sh", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, _, err := SplitHereDocs(tc.header + "\necho sentinel\nEOF\n" + tc.tail + "\n")
			if err != nil || strings.Contains(code, "echo sentinel") != tc.want {
				t.Fatalf("code %q, error %v; want body %v", code, err, tc.want)
			}
		})
	}
}

// TestSplitHereDocsRound3Writes pins the definition visible at each invocation.
func TestSplitHereDocsRound3Writes(t *testing.T) {
	for _, tc := range []struct {
		name, middle, writer string
		wantFirst            bool
	}{
		{"T5 overwrite", "", "cat >", false},
		{"T5 append", "", "cat >>", true},
		{"T5 tee append", "", "tee -a", true},
		{"T5 quoted tee append", "", "tee '-a'", true},
		{"T5 intermediate invocation", "bash run.sh\n", "cat >", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			script := "cat > run.sh <<'A'\necho first\nA\n" + tc.middle + tc.writer + " run.sh <<'B'\necho second\nB\nbash run.sh\n"
			code, _, err := SplitHereDocs(script)
			if err != nil || strings.Contains(code, "echo first") != tc.wantFirst || !strings.Contains(code, "echo second") {
				t.Fatalf("write lifetime: code %q, error %v", code, err)
			}
		})
	}
}

// TestSplitHereDocsRound3Unproven checks named uncertainty without execution credit.
func TestSplitHereDocsRound3Unproven(t *testing.T) {
	for _, tc := range []struct{ name, script, reason string }{
		{"owner", "mystery <<'EOF'\necho sentinel\nEOF\n", "unproven here-document owner: mystery"},
		{"descriptor chain", "bash <<'EOF' <&3\necho sentinel\nEOF\n", "unproven redirect target or descriptor chain"},
		{"directory", "cat > run.sh <<'EOF'\necho sentinel\nEOF\ncd \"$D\"\nbash run.sh\n", "unproven directory change"},
		{"grouped definition", "(cd sub\ncat > run.sh <<'EOF'\necho sentinel\nEOF\n)\nbash run.sh\n", "unproven grouped file definition"},
		{"append without base", "tee -a run.sh <<'EOF'\necho sentinel\nEOF\nbash run.sh\n", "unproven append without file definition"},
		{"piped file definition", "cat <<'EOF' | tee run.sh\necho sentinel\nEOF\nbash run.sh\n", "unproven piped file definition"},
		{"cat file operands", "cat -- > run.sh <<'EOF'\necho sentinel\nEOF\nbash run.sh\n", "unproven cat stdin operands"},
		{"variable tee operand", "tee \"$SCRIPT\" <<'EOF'\necho sentinel\nEOF\nbash run.sh\n", "unproven tee arguments"},
		{"variable invocation", "cat > run.sh <<'EOF'\necho sentinel\nEOF\nbash \"$SCRIPT\"\n", "unproven script invocation"},
		{"variable write", "cat > \"$OUT\" <<'EOF'\necho sentinel\nEOF\nbash run.sh\n", "unproven redirect target"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, docs, err := SplitHereDocs(tc.script)
			if err != nil || len(docs) != 1 || strings.Contains(code, "echo sentinel") || !strings.Contains(docs[0].Reason, tc.reason) {
				t.Fatalf("unproven: code %q, docs %#v, error %v", code, docs, err)
			}
		})
	}
}

// TestHereDocLiteralWords covers quoted literal bytes and expansion/glob refusal.
func TestHereDocLiteralWords(t *testing.T) {
	for _, tc := range []struct{ word, want string }{
		{"'build+sbom.sh'", "build+sbom.sh"}, {`"build+sbom.sh"`, "build+sbom.sh"},
		{"'!$`*?[]{}|&;<> ()\\'", "!$`*?[]{}|&;<> ()\\"},
		{`"a\$b\` + "`" + `c\"d\\e\q"`, "a$b`c\"d\\e\\q"},
		{`"price$.sh"`, "price$.sh"}, {`"price$"`, "price$"},
		{`"$SCRIPT"`, ""}, {`"$(echo file)"`, ""}, {"run*.sh", ""}, {"~user/run", ""},
	} {
		t.Run(tc.word, func(t *testing.T) {
			if got := literalHereDocWord(tc.word); got != tc.want {
				t.Fatalf("decoded %q; want %q", got, tc.want)
			}
		})
	}
}

// TestSplitHereDocsRound3Redirects pins stdin descriptors and parent directory boundaries.
func TestSplitHereDocsRound3Redirects(t *testing.T) {
	for _, tc := range []struct {
		name, header string
		want         bool
	}{
		{"explicit stdin", "bash 0<<'EOF'", true},
		{"other descriptor", "bash 2<<'EOF'", false},
		{"later input replaces body", "bash <<'EOF' < input", false},
		{"earlier input replaced", "bash < input <<'EOF'", true},
		{"stderr independent", "bash <<'EOF' 2> error.log", true},
		{"comment redirect literal", "bash <<'EOF' # > output", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, _, err := SplitHereDocs(tc.header + "\necho sentinel\nEOF\n")
			if err != nil || strings.Contains(code, "echo sentinel") != tc.want {
				t.Fatalf("code %q, error %v; want %v", code, err, tc.want)
			}
		})
	}
}

// TestSplitHereDocsRound3UnprovenWrites withholds stale definitions after unsupported writes.
func TestSplitHereDocsRound3UnprovenWrites(t *testing.T) {
	for _, tc := range []struct{ name, write, reason string }{
		{"multiple outputs", "cat > run.sh > other.sh <<'B'\ntrue\nB\n", "unproven multiple output redirects"},
		{"variable tee writer", "tee \"$SCRIPT\" <<'B'\ntrue\nB\n", "unproven tee arguments or file operand"},
		{"other writer", "printf true > run.sh\n", "unproven non-here-document file write"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, docs, err := SplitHereDocs("cat > run.sh <<'A'\necho sentinel\nA\n" + tc.write + "bash run.sh\n")
			if err != nil || len(docs) == 0 || strings.Contains(code, "echo sentinel") || docs[0].Reason != tc.reason {
				t.Fatalf("unproven write: code %q, docs %#v, error %v", code, docs, err)
			}
		})
	}
}

// TestHereDocDefinitionBound proves exact-cap file fragments and refuses the next definition.
func TestHereDocDefinitionBound(t *testing.T) {
	for _, tc := range []struct {
		name   string
		copies int
	}{{"exact cap", maxShellHeredocs}, {"overflow", maxShellHeredocs + 1}} {
		t.Run(tc.name, func(t *testing.T) {
			script := "cat > run.sh <<'A'\nA\ntee -a " + strings.Repeat("run.sh ", tc.copies-1) + "<<'EOF'\necho sentinel\nEOF\nbash run.sh\n"
			code, docs, err := SplitHereDocs(script)
			if tc.copies > maxShellHeredocs {
				if err == nil || code != "" || docs != nil {
					t.Fatalf("definition overflow returned partial proof: %q, %#v, %v", code, docs, err)
				}
				return
			}
			if err != nil || strings.Count(code, "echo sentinel") != tc.copies-1 {
				t.Fatalf("exact definition cap: %q, %v", code, err)
			}
		})
	}
}

// TestSplitHereDocsRound4Wrappers pins proven data wrappers and keeps wrapped shells non-data.
func TestSplitHereDocsRound4Wrappers(t *testing.T) {
	for _, tc := range []struct {
		name, header       string
		wantCode, wantData bool
	}{
		{"sudo tee data", "sudo tee out.md <<'EOF'", false, true},
		{"doas tee data", "doas -u root tee -a out.md <<'EOF'", false, true},
		{"env cat data", "env -i -u OLD FOO=1 cat <<'EOF'", false, true},
		{"cat pipe tee data", "cat <<'EOF' | tee -a file", false, true},
		{"sudo sh command", "sudo sh -c 'tee out.md' <<'EOF'", false, false},
		{"sudo user bash", "sudo -u x bash <<'EOF'", true, false},
		{"env bash", "env FOO=1 bash <<'EOF'", true, false},
		{"cat pipe sh", "cat <<'EOF' | sh", true, false},
		{"cat pipe sudo bash", "cat <<'EOF' | sudo bash", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, docs, err := SplitHereDocs(tc.header + "\necho sentinel\nEOF\n")
			if err != nil || len(docs) != 1 || strings.Contains(code, "echo sentinel") != tc.wantCode || docs[0].Data() != tc.wantData {
				t.Fatalf("wrapper classification: code %q, docs %#v, err %v", code, docs, err)
			}
		})
	}
}
