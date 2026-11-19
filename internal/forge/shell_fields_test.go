package forge

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/hiss"
)

// TestShellFields checks shell words and code or data here-documents.
func TestShellFields(t *testing.T) {
	for _, tc := range []struct {
		name, script string
		want         []string
	}{
		{"data cd hidden", "cat <<'EOF'\ncd /tmp\nEOF\ngh pr comment 1", []string{"cat", ";", "gh", "pr", "comment", "1"}},
		{"data sbom hidden", "cat <<'EOF'\nsyft packages\nEOF", []string{"cat", ";"}},
		{"piped shell code", "cat <<'EOF' | bash\nsyft dir:. -o sbom.json\nEOF", []string{"cat", "|", "bash", ";", "syft", "dir:.", "-o", "sbom.json", ";"}},
		{"non shell pipe data", "cat <<'EOF' | tee file\nreviewer's syft dir:. -o sbom.json\nEOF", []string{"cat", "|", "tee", "file", ";"}},
		{"shell heredoc code", "bash <<'EOF'\ngh pr view 1\nEOF", []string{"bash", ";", "gh", "pr", "view", "1", ";"}},
		{"cat heredoc data", "cat <<'EOF'\nreviewer's\nEOF", []string{"cat", ";"}},
		{"assignment cat data", "FOO=1 cat > out.md <<'EOF'\nreviewer's\nEOF", []string{"FOO=1", "cat", ">", "out.md", ";"}},
		{"absolute cat data", "/usr/bin/cat <<'EOF'\nreviewer's\nEOF", []string{"/usr/bin/cat", ";"}},
		{"sudo tee data", "sudo tee out.md <<'EOF'\ngh pr view 1\nEOF", []string{"sudo", "tee", "out.md", ";"}},
		{"sudo chdir tee data", "sudo -D /srv tee out.md <<'EOF'\ngh pr view 1\nEOF", []string{"sudo", "-D", "/srv", "tee", "out.md", ";"}},
		{"sudo long chdir tee data", "sudo --chdir=/srv tee out.md <<'EOF'\ngh pr view 1\nEOF", []string{"sudo", "--chdir=/srv", "tee", "out.md", ";"}},
		{"doas tee data", "doas -u root tee -a out.md <<'EOF'\ngh pr view 1\nEOF", []string{"doas", "-u", "root", "tee", "-a", "out.md", ";"}},
		{"env cat data", "env -u BAR FOO=1 cat <<'EOF'\ngh pr view 1\nEOF", []string{"env", "-u", "BAR", "FOO=1", "cat", ";"}},
		{"piped tee options data", "cat <<'EOF' | tee -a -i file\ngh pr view 1\nEOF", []string{"cat", "|", "tee", "-a", "-i", "file", ";"}},
		{"sudo user bash code", "sudo -u x bash <<'EOF'\ngh pr view 1\nEOF", []string{"sudo", "-u", "x", "bash", ";", "gh", "pr", "view", "1", ";"}},
		{"env bash code", "env FOO=1 bash <<'EOF'\ngh pr view 1\nEOF", []string{"env", "FOO=1", "bash", ";", "gh", "pr", "view", "1", ";"}},
		{"piped sh code", "cat <<'EOF' | sh\ngh pr view 1\nEOF", []string{"cat", "|", "sh", ";", "gh", "pr", "view", "1", ";"}},
		{"piped sudo bash code", "cat <<'EOF' | sudo bash\ngh pr view 1\nEOF", []string{"cat", "|", "sudo", "bash", ";", "gh", "pr", "view", "1", ";"}},
		{"heredoc", "cat <<'EOF'\nreviewer's note\nEOF\ngh pr view 1", []string{"cat", ";", "gh", "pr", "view", "1"}},
		{"two heredocs", "cat <<A <<-B\none\nA\n\ttwo\n\tB\n", []string{"cat", ";"}},
		{"delimiter trailing space", "cat <<EOF\nEOF \nreviewer's\nEOF\n", []string{"cat", ";"}},
		{"operators", "cd x;git status&&cd y||true|cat\nfalse", []string{"cd", "x", ";", "git", "status", "&&", "cd", "y", "||", "true", "|", "cat", ";", "false"}},
		{"quotes", `git -C "a  b; c" -c 'key=x|y' status`, []string{"git", "-C", `"a  b; c"`, "-c", "'key=x|y'", "status"}},
		{"comments", "git status # cd hidden\ncd real", []string{"git", "status", ";", "cd", "real"}},
		{"continuation", "git sta\\\ntus", []string{"git", "status"}},
		{"escaped", `echo a\;b \"x`, []string{"echo", `a\;b`, `\"x`}},
		{"20w token line", `token="$(printf '%s' "$token_response" | sed -n 's/.*"token":"\([^"]*\)".*/\1/p')"`, []string{`token="$(printf '%s' "$token_response" | sed -n 's/.*"token":"\([^"]*\)".*/\1/p')"`}},
		{"nested command substitution", `"$(echo "x y")"`, []string{`"$(echo "x y")"`}},
		{"printf command substitution", `"$(printf '%s' "x")"`, []string{`"$(printf '%s' "x")"`}},
		{"backquote form", `token="` + "`" + `printf '%s' "$token_response" | sed -n 's/.*"token":"\([^"]*\)".*/\1/p'` + "`" + `"`, []string{`token="` + "`" + `printf '%s' "$token_response" | sed -n 's/.*"token":"\([^"]*\)".*/\1/p'` + "`" + `"`}},
		{"backquote echo", `"` + "`" + `echo "x y"` + "`" + `"`, []string{`"` + "`" + `echo "x y"` + "`" + `"`}},
		{"empty", "", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ShellFields(tc.script)
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("ShellFields = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}

// TestShellWrapperValueOptionsShared compares both readers' sudo value sets and
// retains the forge reader's env arities, including unproven here-document options.
func TestShellWrapperValueOptionsShared(t *testing.T) {
	for _, wrapper := range []string{"sudo", "env"} {
		want := hiss.ShellWrapperOptions[wrapper].ValueOptions()
		if got := commandWrappers[wrapper].valueOptions; !reflect.DeepEqual(got, want) {
			t.Fatalf("%s forge value options %v; shared %v", wrapper, got, want)
		}
		for option := range want {
			cmd := parseShellCommand([]string{wrapper, option, "/srv", "tee", "out.md"})
			if cmd.program != "tee" {
				t.Errorf("%s %s value: forge program %q; want tee", wrapper, option, cmd.program)
			}
			if wrapper == "sudo" {
				assertWrapperHereDocData(t, wrapper+" "+option+" /srv", true)
			}
		}
	}
	wantLetters := hiss.ShellWrapperOptionSpec{ValueShort: hiss.ShellWrapperOptions["sudo"].ValueShort}.ValueOptions()
	gotLetters := hiss.ShellWrapperOptionSpec{ValueShort: sudoValueLetters}.ValueOptions()
	if !reflect.DeepEqual(gotLetters, wantLetters) {
		t.Fatalf("forge sudo value letters %v; shared %v", gotLetters, wantLetters)
	}
	if options := (hiss.ShellWrapperOptionSpec{}).ValueOptions(); len(options) != 0 {
		t.Fatalf("empty wrapper spec has options: %v", options)
	}
}

// TestShellWrapperHereDocAllowlist keeps unknown and transforming options unproven.
// Missing/empty operands and bare options pin the arity boundaries.
func TestShellWrapperHereDocAllowlist(t *testing.T) {
	for _, prefix := range []string{
		"sudo --unknown", "sudo -Z", "env -S value", "env --split-string=value",
		"env -C /srv", "env --chdir=/srv", "env -a name", "env --argv0=name",
		"sudo --chdir=", "env --unset=", "doas --user root",
	} {
		assertWrapperHereDocData(t, prefix, false)
	}
	for _, prefix := range []string{"sudo -D/srv", "sudo --chdir=/srv", "env -", "sudo --preserve-env=FOO"} {
		assertWrapperHereDocData(t, prefix, true)
	}
	for _, prefix := range []string{"sudo -D", "sudo --chdir", "env -u", "doas -u"} {
		_, docs, err := hiss.SplitHereDocs(prefix + " <<'EOF'\ndata\nEOF\n")
		if err != nil || len(docs) != 1 || docs[0].Data() {
			t.Fatalf("missing wrapper operand %q: docs %#v, err %v", prefix, docs, err)
		}
	}
}

func assertWrapperHereDocData(t *testing.T, prefix string, want bool) {
	t.Helper()
	_, docs, err := hiss.SplitHereDocs(prefix + " tee out.md <<'EOF'\ngh pr view 1\nEOF\n")
	if err != nil || len(docs) != 1 || docs[0].Data() != want {
		t.Fatalf("wrapper %q: docs %#v, err %v; want data %v", prefix, docs, err, want)
	}
}

// TestShellWrapperOptionSources refuses private option literals in either reader's
// arity declarations, even when a copied list initially has the same contents.
func TestShellWrapperOptionSources(t *testing.T) {
	for _, tc := range []struct{ path, name, key string }{
		{"build_warnings_shell.go", "commandWrappers", "sudo"},
		{"build_warnings_shell.go", "commandWrappers", "env"},
		{"build_warnings_shell.go", "sudoValueLetters", ""},
		{"../hiss/shell_heredoc_files.go", "hereDocWrapperOption", ""},
	} {
		t.Run(tc.name+tc.key, func(t *testing.T) {
			file, err := parser.ParseFile(token.NewFileSet(), tc.path, nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			node := wrapperOptionSource(t, file, tc.name, tc.key)
			ast.Inspect(node, func(node ast.Node) bool {
				lit, ok := node.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				value, err := strconv.Unquote(lit.Value)
				if err != nil || !slices.Contains([]string{"", "sudo", "env", "--", "="}, value) {
					t.Errorf("private wrapper option literal %s; use hiss.ShellWrapperOptions", lit.Value)
				}
				return true
			})
		})
	}
}

func wrapperOptionSource(t *testing.T, file *ast.File, name, key string) ast.Node {
	t.Helper()
	var found ast.Node
	ast.Inspect(file, func(node ast.Node) bool {
		if fn, ok := node.(*ast.FuncDecl); ok && fn.Name.Name == name {
			found = fn.Body
		}
		if decl, ok := node.(*ast.ValueSpec); ok && len(decl.Names) == 1 && decl.Names[0].Name == name {
			found = decl.Values[0]
		}
		return true
	})
	if found == nil {
		t.Fatalf("wrapper option source missing: %s", name)
	}
	if key != "" {
		found = wrapperOptionEntry(t, found, key)
	}
	return found
}

func wrapperOptionEntry(t *testing.T, node ast.Node, key string) ast.Node {
	t.Helper()
	lit, ok := node.(*ast.CompositeLit)
	if !ok {
		t.Fatalf("wrapper table must contain entries: %T", node)
	}
	for _, entry := range lit.Elts {
		pair, ok := entry.(*ast.KeyValueExpr)
		if ok {
			if word, ok := pair.Key.(*ast.BasicLit); ok && word.Value == strconv.Quote(key) {
				return pair.Value
			}
		}
	}
	t.Fatalf("wrapper option entry missing: %s", key)
	return nil
}

// TestShellFieldsBoundaries rejects oversized and malformed scripts.
func TestShellFieldsBoundaries(t *testing.T) {
	if got, err := ShellFields(strings.Repeat("x ", maxRunScriptFields)); err != nil || len(got) != maxRunScriptFields {
		t.Fatalf("at bound: %d fields, %v", len(got), err)
	}
	for _, script := range []string{
		strings.Repeat("x ", maxRunScriptFields) + "x",
		strings.Repeat("x ", maxRunScriptFields+1), `git -C "unclosed`, "cat <<EOF\nmissing\n", `cat <<<'it's'`,
		"bash <<EOF\nreviewer's\nEOF\n",
		"sudo sh -c 'tee out.md' <<'EOF'\ngh pr view 1\nEOF\n",
	} {
		if got, err := ShellFields(script); err == nil || got != nil {
			t.Errorf("unreadable script accepted: %d fields, %v", len(got), err)
		}
	}
	if _, err := ShellFields(strings.Repeat("x ", maxRunScriptFields+1)); !errors.Is(err, ErrShellReadLimit) {
		t.Fatalf("field overflow must be identifiable: %v", err)
	}
}

func TestShellAssignment(t *testing.T) {
	for _, tc := range []struct {
		word, name, value string
		ok                bool
	}{
		{`GIT_DIR="a b/.git"`, "GIT_DIR", `"a b/.git"`, true},
		{"X=", "X", "", true}, {"git", "", "", false}, {"--git-dir=x", "", "", false},
	} {
		t.Run(tc.word, func(t *testing.T) {
			name, value, ok := ShellAssignment(tc.word)
			if name != tc.name || value != tc.value || ok != tc.ok {
				t.Fatalf("assignment = %q %q %v", name, value, ok)
			}
		})
	}
}

func TestShellFieldsLineLimit(t *testing.T) {
	atBound := "\"" + strings.Repeat("\n", 99999) + "\""
	if fields, err := ShellFields(atBound); err != nil || len(fields) != 1 {
		t.Fatalf("100000 lines: %d fields, %v", len(fields), err)
	}
	if _, err := ShellFields(atBound + "\n"); !errors.Is(err, ErrShellReadLimit) {
		t.Fatalf("line overflow must be identifiable: %v", err)
	}
}

func TestShellFields_NestedQuoteErrors(t *testing.T) {
	unclosed := []struct {
		name, script string
	}{
		{"real unclosed quote", `echo "abc`},
		{"unclosed substitution quote", `"$(echo "x)"`},
		{"unclosed inner substitution", `"$(echo "x"`},
	}
	for _, tc := range unclosed {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ShellFields(tc.script)
			if err == nil || got != nil {
				t.Fatalf("ShellFields(%s) accepted: %v", tc.name, got)
			}
			if !strings.Contains(err.Error(), "run script has an unclosed quote") {
				t.Fatalf("error = %v; want unclosed quote", err)
			}
		})
	}

	var pastBound strings.Builder
	for i := 0; i < 17; i++ {
		pastBound.WriteString(`"$(`)
	}
	if _, err := ShellFields(pastBound.String()); !errors.Is(err, ErrShellReadLimit) {
		t.Fatalf("nesting overflow must return ErrShellReadLimit, got: %v", err)
	}
}

// checkHereDocReader exercises shared execution and data paths in a reader's own tests.
func checkHereDocReader(t *testing.T, body string, read func(string) bool) {
	t.Helper()
	for _, tc := range []struct {
		name, script string
		want         bool
	}{
		{"cat pipe", "cat <<'EOF' | bash\n" + body + "\nEOF\n", true},
		{"tee pipe", "tee copy <<'EOF' | bash\n" + body + "\nEOF\n", true},
		{"shell stdin", "bash <<'EOF'\n" + body + "\nEOF\n", true},
		{"data", "cat > note <<'EOF'\n" + body + "\nEOF\n", false},
		{"script file", "cat > run.sh <<'EOF'\n" + body + "\nEOF\nbash run.sh\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := read(tc.script); got != tc.want {
				t.Fatalf("reader got %v; want %v", got, tc.want)
			}
		})
	}
}
