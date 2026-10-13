package adopt

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Tests for #1264: a test-recipe script that only runs the other one is not run a second time.

func nodeAliasPlan(t *testing.T, scripts string) *VerificationPlan {
	t.Helper()
	_, plan := verificationFixture(t, map[string]string{
		"package.json": `{"packageManager":"npm@11.15.0","scripts":` + scripts + `}`,
	})
	return plan
}

func npmRuns(plan *VerificationPlan) []string {
	var names []string
	for _, group := range [][][]string{plan.Build, plan.Test} {
		for _, command := range group {
			if len(command) == 3 && command[0] == "npm" && command[1] == "run" {
				names = append(names, command[2])
			}
		}
	}
	return names
}

func TestNodeAlias_Positive_CheckThatRunsTestIsLeftOut(t *testing.T) {
	plan := nodeAliasPlan(t, `{"build":"node count.mjs build","check":"npm test","test":"node count.mjs test"}`)
	if got := npmRuns(plan); !reflect.DeepEqual(got, []string{"build", "test"}) {
		t.Fatalf("recipes must run build and test once each, got %v", got)
	}
	if plan.Status != verificationDeclared {
		t.Fatalf("plan status changed: %+v", plan)
	}
	note := strings.Join(plan.SelectedBy, "; ")
	if !strings.Contains(note, "npm run check left out: package.json scripts.check only runs scripts.test (npm test)") {
		t.Fatalf("the report must say why check is not run: %q", note)
	}
	makefile := buildMakefile(plan)
	if strings.Count(makefile, "'npm' 'run' 'test'") != 1 || strings.Contains(makefile, "'npm' 'run' 'check'") {
		t.Fatalf("rendered recipe runs the suite twice:\n%s", makefile)
	}
}

func TestNodeAlias_Positive_EveryAliasForm(t *testing.T) {
	for _, body := range []string{
		"npm test", "npm t", "npm tst", "npm run test", "npm run-script test",
		"pnpm test", "pnpm t", "pnpm run test", "yarn test", "yarn run test",
		"  npm test  ", "npm run 'test'",
	} {
		plan := nodeAliasPlan(t, `{"build":"b","check":"`+strings.ReplaceAll(body, `"`, `\"`)+`","test":"node t.mjs"}`)
		if got := npmRuns(plan); !reflect.DeepEqual(got, []string{"build", "test"}) {
			t.Errorf("check = %q: got %v, want [build test]", body, got)
		}
	}
}

func TestNodeAlias_Positive_TestThatRunsCheckIsLeftOut(t *testing.T) {
	plan := nodeAliasPlan(t, `{"build":"b","check":"node c.mjs","test":"npm run check"}`)
	if got := npmRuns(plan); !reflect.DeepEqual(got, []string{"build", "check"}) || plan.Status != verificationDeclared {
		t.Fatalf("test that only runs check must be left out: %v %+v", got, plan)
	}
}

func TestNodeAlias_Negative_ScriptsThatDoMoreAreKept(t *testing.T) {
	for _, body := range []string{
		"npm test -- --watch", "npm run test && npm run lint", "CI=1 npm test", "env CI=1 npm test",
		"npm run lint", "npm run build", "yarn check", "pnpm check", "npx test", "yarn t", "node c.mjs",
	} {
		plan := nodeAliasPlan(t, `{"build":"b","check":"`+body+`","test":"node t.mjs"}`)
		if got := npmRuns(plan); !reflect.DeepEqual(got, []string{"build", "check", "test"}) {
			t.Errorf("check = %q must stay: got %v", body, got)
		}
		if len(plan.SelectedBy) != 0 {
			t.Errorf("check = %q: no note expected, got %v", body, plan.SelectedBy)
		}
	}
}

func TestNodeAlias_Boundary_CycleMissingTargetAndBuild(t *testing.T) {
	cases := []struct {
		name, scripts string
		want          []string
	}{
		{"scripts that alias each other both stay", `{"build":"b","check":"npm test","test":"npm run check"}`, []string{"build", "check", "test"}},
		{"an alias of a missing test stays", `{"build":"b","check":"npm test"}`, []string{"build", "check"}},
		{"an alias of the npm init placeholder stays", `{"build":"b","check":"npm test","test":"echo \"Error: no test specified\" && exit 1"}`, []string{"build", "check"}},
		{"build that runs test stays", `{"build":"npm test","check":"node c.mjs","test":"node t.mjs"}`, []string{"build", "check", "test"}},
		{"test that runs build stays", `{"build":"node b.mjs","test":"npm run build"}`, []string{"build", "test"}},
	}
	for _, tc := range cases {
		if got := npmRuns(nodeAliasPlan(t, tc.scripts)); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestNodeAlias_PlantedStubRunsTheSuiteOnce is the measurement from #1264: each script appends
// its name to a file, the generated build and test recipes run as make runs them, and the suite
// runs once; the control with a check that does its own work runs each script once.
func TestNodeAlias_PlantedStubRunsTheSuiteOnce(t *testing.T) {
	if _, err := exec.LookPath("npm"); err != nil {
		t.Skip("npm is not on PATH; the planted stub runs where npm and node are installed")
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not on PATH; the planted stub runs where npm and node are installed")
	}
	cases := map[string]string{
		`npm test`:             "build\ntest\n",
		`node count.mjs check`: "build\ncheck\ntest\n",
	}
	for check, want := range cases {
		root, plan := verificationFixture(t, map[string]string{
			"package.json": `{"packageManager":"npm@11.15.0","scripts":{"build":"node count.mjs build","check":"` + check + `","test":"node count.mjs test"}}`,
			"count.mjs":    "import { appendFileSync } from 'node:fs';\nappendFileSync('runs.txt', process.argv[2] + '\\n');\n",
		})
		// make test: build first, then the test recipe.
		for _, command := range append(append([][]string{}, plan.Build...), plan.Test...) {
			runPlannedCommand(t, root, command)
		}
		got, err := os.ReadFile(filepath.Join(root, "runs.txt"))
		if err != nil || strings.ReplaceAll(string(got), "\r\n", "\n") != want {
			t.Errorf("check = %q: runs %q (%v), want %q", check, got, err, want)
		}
	}
}

func runPlannedCommand(t *testing.T, root string, command []string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v: %v\n%s", command, err, out)
	}
}
