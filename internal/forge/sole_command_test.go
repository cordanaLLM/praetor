package forge_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/forge"
)

func TestSoleCommand_Positive_OnePlainCommand(t *testing.T) {
	cases := map[string][]string{
		"npm test":             {"npm", "test"},
		"  npm run  'test'  ":  {"npm", "run", "test"},
		`pnpm run "check"`:     {"pnpm", "run", "check"},
		"NPM.exe test":         {"npm", "test"},
		"node count.mjs build": {"node", "count.mjs", "build"},
	}
	for script, want := range cases {
		program, args, ok := forge.SoleCommand(script)
		if !ok || program != want[0] || !slices.Equal(args, want[1:]) {
			t.Errorf("%q: got %q %q ok=%v, want %q", script, program, args, ok, want)
		}
	}
}

func TestSoleCommand_Negative_MoreThanTheProgram(t *testing.T) {
	for _, script := range []string{
		"npm test && npm run lint",
		"npm test; npm run lint",
		"npm test | tee log",
		"npm test &",
		"CI=1 npm test",
		"env CI=1 npm test",
		"nohup npm test",
		"if npm test; then :; fi",
		"",
		"   ",
	} {
		if program, args, ok := forge.SoleCommand(script); ok {
			t.Errorf("%q must not read as one plain command, got %q %q", script, program, args)
		}
	}
}

func TestSoleCommand_Boundary_LayoutAroundOneCommand(t *testing.T) {
	for _, script := range []string{"npm test;", "npm test # the suite", "npm \\\n  test", "(npm test)", "npm test\n"} {
		program, args, ok := forge.SoleCommand(script)
		if !ok || program != "npm" || !slices.Equal(args, []string{"test"}) {
			t.Errorf("%q: got %q %q ok=%v, want npm [test]", script, program, args, ok)
		}
	}
	if _, _, ok := forge.SoleCommand("npm " + strings.Repeat("x ", 100000)); ok {
		t.Error("a script over the field bound must not read as a command")
	}
}
