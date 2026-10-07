package forge

import (
	"strings"
	"testing"
)

// The words of one command as scriptCommands cuts them from a one-line script.
func commandWords(t *testing.T, script string) [][]string {
	t.Helper()
	fields, err := scriptFields(script)
	if err != nil {
		t.Fatal(err)
	}
	return scriptCommands(fields)
}

// Positive: a subshell's parentheses are dropped, a ${{ }} expression is one word, and a
// balanced $( ) substitution is kept. Boundary: a lone parenthesis leaves no word.
func TestScriptCommands_Subshells(t *testing.T) {
	cases := map[string]string{
		"(cd build && cmake .. -DX=ON)":     "cd build|cmake .. -DX=ON",
		"( cd build; make -j$(nproc) )":     "cd build|make -j$(nproc)",
		"${{ matrix.cc }} -c a.c":           "${{ matrix.cc }} -c a.c",
		"((cmake -B b -DCMAKE_X=1))":        "cmake -B b -DCMAKE_X=1",
		"echo \"$(date)\" && (true)":        "echo $(date)|true",
		"if x; then (cmake -B b -DX=1); fi": "x|cmake -B b -DX=1|fi",
	}
	for script, want := range cases {
		var got []string
		for _, words := range commandWords(t, script) {
			cmd := parseShellCommand(words)
			got = append(got, strings.TrimSpace(cmd.word+" "+strings.Join(cmd.args, " ")))
		}
		if strings.Join(got, "|") != want {
			t.Errorf("%q = %q; want %q", script, strings.Join(got, "|"), want)
		}
	}
}

// Positive: a wrapper's options and operands are passed over to the program it runs, and what
// it does to the environment is recorded: sudo resets it unless -E (alone or in a cluster such
// as -nE) or --preserve-env keeps it (sudo(8), sudoers(5) env_reset), env -i empties it and
// env -u drops one name (env(1)). Negative: an assignment before sudo is hidden by its reset.
// Boundary: a cluster carrying a value (-uEVE) keeps no environment.
func TestParseShellCommand_Wrappers(t *testing.T) {
	cases := []struct {
		script, program string
		hidden, visible []string
	}{
		{"sudo -E cargo build", "cargo", nil, []string{"RUSTFLAGS"}},
		{"sudo -nE cargo build", "cargo", nil, []string{"RUSTFLAGS"}},
		{"sudo -u builder -g wheel cargo build", "cargo", []string{"RUSTFLAGS"}, nil},
		{"sudo --preserve-env=RUSTFLAGS,CL -- cargo build", "cargo", []string{"CC"}, []string{"RUSTFLAGS", "CL"}},
		{"RUSTFLAGS=-Dwarnings sudo cargo build", "cargo", []string{"RUSTFLAGS"}, nil},
		{"sudo -uEVE cargo build", "cargo", []string{"RUSTFLAGS"}, nil},
		{"env -u RUSTFLAGS --unset=CL cargo build", "cargo", []string{"RUSTFLAGS", "CL"}, []string{"CC"}},
		{"env -i PATH=/bin cargo build", "cargo", []string{"RUSTFLAGS"}, []string{"PATH"}},
		{"nice -n 10 time -f %e timeout -s KILL 30m exec -a b C:\\tools\\GCC.EXE -c a.c", "gcc", nil, []string{"CFLAGS"}},
	}
	env := stepEnvironment{script: map[string]string{"RUSTFLAGS": "-Dwarnings", "CL": "/WX", "CC": "gcc", "CFLAGS": "-O2"}}
	for _, tc := range cases {
		cmd := parseShellCommand(commandWords(t, tc.script)[0])
		if cmd.program != tc.program {
			t.Errorf("%q: program %q; want %q", tc.script, cmd.program, tc.program)
		}
		lookup := env.lookup(cmd)
		for _, name := range tc.hidden {
			if _, set, _ := lookup(name); set {
				t.Errorf("%q: %s reaches the program; want it hidden", tc.script, name)
			}
		}
		for _, name := range tc.visible {
			if _, set, _ := lookup(name); !set && name != "PATH" {
				t.Errorf("%q: %s does not reach the program; want it visible", tc.script, name)
			}
		}
	}
}

// A program's base name is cut at a slash or a backslash on every host, so a Windows path reads
// the same on Linux, macOS and Windows (HISS-21); boundary: a bare name and a trailing slash.
func TestCommandName_SlashNeutral(t *testing.T) {
	cases := map[string]string{
		`C:\msys64\mingw64\bin\gcc.exe`: "gcc.exe",
		"./bin/praetorctl":              "praetorctl",
		`.\bin\praetorctl`:              "praetorctl",
		"cosign":                        "cosign",
		"/usr/bin/":                     "bin",
	}
	for field, want := range cases {
		if got := commandName(field); got != want {
			t.Errorf("commandName(%q) = %q; want %q", field, got, want)
		}
	}
}

// A program given as a variable is read from its value, wrappers and all; one holding any other
// expansion, or a variable the step does not set, stays unread.
func TestResolveProgram(t *testing.T) {
	env := stepEnvironment{script: map[string]string{"CC": "ccache clang-18 -std=c17", "CARGO": "$HOME/.cargo/bin/cargo"}}
	cases := map[string]struct {
		program string
		read    bool
	}{
		"$CC -c a.c":            {"clang-18", true},
		"${CC} -c a.c":          {"clang-18", true},
		"$CXX -c a.cpp":         {"$cxx", false},
		"$CARGO build":          {"$cargo", false},
		"${{ matrix.cc }} -c x": {"${{ matrix.cc }}", false},
		"gcc -c a.c":            {"gcc", true},
	}
	for script, want := range cases {
		cmd, read := env.resolveProgram(parseShellCommand(commandWords(t, script)[0]))
		if cmd.program != want.program || read != want.read {
			t.Errorf("%q: program %q, read %t; want %q, %t", script, cmd.program, read, want.program, want.read)
		}
	}
	cmd, _ := env.resolveProgram(parseShellCommand(commandWords(t, "$CC -Werror -c a.c")[0]))
	if strings.Join(cmd.args, " ") != "-std=c17 -Werror -c a.c" {
		t.Errorf("resolved args = %q; want the value's words first", cmd.args)
	}
}
