// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

var pytestCommand = []string{"python3", "-m", "pytest"}

// planRunsPytest reports whether plan selected python3 -m pytest as a test command.
func planRunsPytest(plan *VerificationPlan) bool {
	for _, command := range plan.Test {
		if slices.Equal(command, pytestCommand) {
			return true
		}
	}
	return false
}

// #594: every file pytest itself reads its configuration from selects pytest, and the plan names
// the file, with the table or section that made it pytest's, in pytest's own precedence order.
func TestVerificationPytestConfigurationPositive(t *testing.T) {
	for name, tc := range map[string]struct {
		files map[string]string
		want  string
	}{
		"pyproject-ini-options": {map[string]string{"pyproject.toml": "[project]\nname = \"pytool\"\n\n[tool.pytest.ini_options]\ntestpaths = [\"tests\"]\n"}, "pyproject.toml [tool.pytest.ini_options]"},
		"pyproject-native":      {map[string]string{"pyproject.toml": "[tool.pytest]\naddopts = [\"-ra\"]\n"}, "pyproject.toml [tool.pytest]"},
		"pyproject-spaced":      {map[string]string{"pyproject.toml": "  [ tool.pytest.ini_options ]  # pytest\n"}, "pyproject.toml [tool.pytest.ini_options]"},
		"pytest-toml-empty":     {map[string]string{"pytest.toml": ""}, "pytest.toml"},
		"hidden-pytest-toml":    {map[string]string{".pytest.toml": "[pytest]\n"}, ".pytest.toml"},
		"pytest-ini-empty":      {map[string]string{"pytest.ini": ""}, "pytest.ini"},
		"hidden-pytest-ini":     {map[string]string{".pytest.ini": "[pytest]\n"}, ".pytest.ini"},
		"tox-ini":               {map[string]string{"tox.ini": "[tox]\nenvlist = py314\n\n[pytest]\ntestpaths = tests\n"}, "tox.ini [pytest]"},
		"tox-ini-crlf-comment":  {map[string]string{"tox.ini": "[pytest] ; pytest options\r\ntestpaths = tests\r\n"}, "tox.ini [pytest]"},
		"setup-cfg":             {map[string]string{"setup.cfg": "[metadata]\nname = pytool\n\n[tool:pytest]\ntestpaths = tests\n"}, "setup.cfg [tool:pytest]"},
		"precedence":            {map[string]string{"pytest.ini": "", "pyproject.toml": "[tool.pytest.ini_options]\n", "setup.cfg": "[tool:pytest]\n"}, "pytest.ini"},
	} {
		t.Run(name, func(t *testing.T) {
			_, plan := verificationFixture(t, tc.files)
			if !planRunsPytest(plan) || !slices.Contains(plan.Runtimes, "python") {
				t.Fatalf("pytest configuration not selected: %+v", plan)
			}
			want := "'python3' '-m' 'pytest' from the pytest configuration in " + tc.want
			if !slices.Equal(plan.SelectedBy, []string{want}) {
				t.Fatalf("selected_by = %q, want %q", plan.SelectedBy, want)
			}
			if notice := plan.notice(); !strings.Contains(notice, want) {
				t.Fatalf("notice does not name the configuration file: %s", notice)
			}
		})
	}
}

// Negative and boundary: a file pytest reads only with a pytest table or section is not pytest
// configuration without one, and a header pytest would not read as that section is no section.
func TestVerificationPytestConfigurationNegative(t *testing.T) {
	for name, files := range map[string]map[string]string{
		"pyproject-no-table":       {"pyproject.toml": "[project]\nname = \"pytool\"\n\n[tool.ruff]\nline-length = 100\n"},
		"pyproject-subtable-only":  {"pyproject.toml": "[tool.pytest.ini_options.extra]\nx = 1\n"},
		"pyproject-array-of-table": {"pyproject.toml": "[[tool.pytest]]\nx = 1\n"},
		"pyproject-string-value":   {"pyproject.toml": "[project]\ndescription = \"[tool.pytest]\"\n"},
		"tox-no-section":           {"pyproject.toml": "[project]\n", "tox.ini": "[tox]\nenvlist = py314\n\n[testenv]\ncommands = pytest\n"},
		"tox-spaced-section":       {"pyproject.toml": "[project]\n", "tox.ini": "[ pytest ]\ntestpaths = tests\n"},
		"tox-indented-section":     {"pyproject.toml": "[project]\n", "tox.ini": "[tox]\n [pytest]\n"},
		"tox-setup-cfg-section":    {"pyproject.toml": "[project]\n", "tox.ini": "[tool:pytest]\n"},
		"setup-cfg-tox-section":    {"pyproject.toml": "[project]\n", "setup.cfg": "[pytest]\n"},
		"nested-pytest-ini":        {"pyproject.toml": "[project]\n", "src/pytest.ini": ""},
	} {
		t.Run(name, func(t *testing.T) {
			_, plan := verificationFixture(t, files)
			if planRunsPytest(plan) || len(plan.SelectedBy) != 0 {
				t.Fatalf("pytest selected without pytest configuration: %+v", plan)
			}
			if plan.Status != verificationUnavailable {
				t.Fatalf("plan without a test runner is usable: %+v", plan)
			}
		})
	}
	// A tox.ini or setup.cfg without a pytest section, and nothing else, makes no Python project.
	_, plan := verificationFixture(t, map[string]string{"tox.ini": "[tox]\n", "setup.cfg": "[metadata]\n"})
	if slices.Contains(plan.Runtimes, "python") {
		t.Fatalf("configuration without a pytest section selected Python: %+v", plan)
	}
}

// #594: an unavailable plan's notice says what verify-all does, what is missing, and which
// discovered commands verify-all does not run; a declared plan's notice claims none of it.
func TestVerificationNoticeNamesWhatIsMissing(t *testing.T) {
	_, python := verificationFixture(t, map[string]string{"pyproject.toml": "[tool.pytest.ini_options]\n"})
	notice := python.notice()
	for _, want := range []string{
		"Project verification unavailable: ", "runs the governance checks and then exits 1.",
		"Missing: a build command.", "Discovered but not written to verify-all: 'python3' '-m' 'pytest'.",
	} {
		if !strings.Contains(notice, want) {
			t.Fatalf("notice lacks %q: %s", want, notice)
		}
	}
	if strings.Contains(notice, "does not identify a runnable test gate") {
		t.Fatalf("notice denies the pytest configuration it found: %s", notice)
	}
	_, empty := verificationFixture(t, map[string]string{})
	if notice := empty.notice(); !strings.Contains(notice, "Missing: a build command and a test command.") || strings.Contains(notice, "Discovered") {
		t.Fatalf("empty plan notice: %s", notice)
	}
	_, declared := verificationFixture(t, map[string]string{"go.mod": "module fixture\n"})
	if notice := declared.notice(); strings.Contains(notice, "exits 1") || strings.Contains(notice, "Missing") || strings.Contains(notice, "Selected by") {
		t.Fatalf("declared plan notice claims a failure: %s", notice)
	}
}

// #594: an appended verify-all holds the failing pair once for an unavailable plan, and the build
// commands before the test commands, each once, for a declared one.
func TestAppendedVerifyAllRendersOneRecipe(t *testing.T) {
	existing := ".PHONY: test\ntest:\n\tpython3 -m pytest\n"
	unavailable := &VerificationPlan{Status: verificationUnavailable, Build: [][]string{}, Test: [][]string{pytestCommand}}
	got, err := appendVerificationTargets(existing, unavailable)
	if err != nil {
		t.Fatal(err)
	}
	if count := strings.Count(got, unavailableVerificationRecipe); count != 1 {
		t.Fatalf("failing pair written %d times:\n%s", count, got)
	}
	declared := &VerificationPlan{Status: verificationDeclared, Build: [][]string{{"cargo", "build"}}, Test: [][]string{{"cargo", "test"}}}
	got, err = appendVerificationTargets(existing, declared)
	if err != nil {
		t.Fatal(err)
	}
	build, test := strings.Index(got, "\t@exec 'cargo' 'build'\n"), strings.Index(got, "\t@exec 'cargo' 'test'\n")
	if build < 0 || test < build || strings.Contains(got, unavailableVerificationRecipe) {
		t.Fatalf("declared commands not rendered in order:\n%s", got)
	}
	// Boundary: a declared plan carrying an empty command still fails, once.
	declared.Test = [][]string{{}}
	if got, err = appendVerificationTargets(existing, declared); err != nil || strings.Count(got, unavailableVerificationRecipe) != 1 {
		t.Fatalf("empty command recipe: %v\n%s", err, got)
	}
}

// A Makefile adoption does not recognise as its current output that still holds the failing
// placeholder recipe keeps the plan unavailable, CRLF or not; a custom verify-all without it is
// preserved.
func TestPreservedVerifyAllWithPlaceholderStaysUnavailable(t *testing.T) {
	placeholder := "test:\n\tpython3 -m pytest\n\nverify-all:\n\t@$(PRAETORCTL) audit\n" + unavailableVerificationRecipe
	for name, data := range map[string]string{"lf": placeholder, "crlf": strings.ReplaceAll(placeholder, "\n", "\r\n")} {
		plan := &VerificationPlan{Status: verificationDeclared, Build: [][]string{{"go", "build"}}, Test: [][]string{{"go", "test"}}}
		preserveCustomVerification(plan, []byte(data))
		if plan.Status != verificationUnavailable || len(plan.Reasons) != 1 || !strings.Contains(plan.Reasons[0], "failing placeholder") {
			t.Fatalf("%s: placeholder verify-all preserved: %+v", name, plan)
		}
		if len(plan.Test) != 1 || plan.Test[0][0] != "go" {
			t.Fatalf("%s: discovered commands dropped: %+v", name, plan)
		}
	}
	custom := &VerificationPlan{Status: verificationDeclared, Build: [][]string{{"go", "build"}}, Test: [][]string{{"go", "test"}}}
	preserveCustomVerification(custom, []byte("verify-all:\n\t@echo custom\n"))
	if custom.Status != verificationPreserved {
		t.Fatalf("custom verify-all not preserved: %+v", custom)
	}
}

// #594: the Verification Gate follows the verify-all adoption writes. An unavailable plan warns
// a completed makefile step, in a dry run too, and the pillar carries the notice; a failed,
// declined or unreached step keeps its own status, and a declared plan leaves the pillar ready.
func TestVerificationPillarFollowsThePlan(t *testing.T) {
	unavailable := &VerificationPlan{Status: verificationUnavailable, Reasons: []string{"A required build or test command is absent."}, Test: [][]string{pytestCommand}}
	for _, tc := range []struct {
		name   string
		dryRun bool
		plan   *VerificationPlan
		status StepStatus
		want   PillarStatus
	}{
		{"unavailable-applied", false, unavailable, StepCompleted, PillarWarned},
		{"unavailable-dry-run", true, unavailable, StepCompleted, PillarWarned},
		{"unavailable-declined", false, unavailable, StepDeclined, PillarDeclined},
		{"unavailable-failed", false, unavailable, StepFailed, PillarFailed},
		{"declared", false, &VerificationPlan{Status: verificationDeclared}, StepCompleted, PillarReady},
		{"preserved", false, &VerificationPlan{Status: verificationPreserved}, StepCompleted, PillarReady},
		{"no-plan", false, nil, StepCompleted, PillarReady},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &AdoptReport{DryRun: tc.dryRun, Verification: tc.plan}
			r.recordStep(verificationStep, tc.status, r.mark())
			pillar := pillarNamed(t, r, "Verification Gate")
			if pillar.Status != tc.want {
				t.Fatalf("Verification Gate = %s, want %s", pillar.Status, tc.want)
			}
			carries := slices.ContainsFunc(pillar.Warnings, func(w string) bool { return strings.Contains(w, "exits 1") })
			if carries != (tc.want == PillarWarned) {
				t.Fatalf("pillar warnings %q", pillar.Warnings)
			}
			pending := slices.Contains(r.PendingPillars(), "Verification Gate")
			if pending != (tc.want == PillarWarned || tc.want == PillarFailed) {
				t.Fatalf("PendingPillars() = %q", r.PendingPillars())
			}
		})
	}
	// Boundary: an unreached makefile step stays not-run, and a step that warned keeps its own
	// warning beside the notice, which the step itself does not gain.
	unreached := &AdoptReport{Verification: unavailable}
	if got := pillarNamed(t, unreached, "Verification Gate"); got.Status != PillarNotRun {
		t.Fatalf("unreached pillar = %s", got.Status)
	}
	warned := &AdoptReport{Verification: unavailable, Steps: []StepOutcome{{Name: verificationStep, Status: StepCompleted, Warnings: []string{"step warning"}}}}
	if got := pillarNamed(t, warned, "Verification Gate"); got.Status != PillarWarned || len(got.Warnings) != 2 || len(warned.Steps[0].Warnings) != 1 {
		t.Fatalf("step warnings mutated or notice missing: %+v %+v", got, warned.Steps[0])
	}
	// A declined pillar is the manifest's decision, not pending work.
	declined := &AdoptReport{}
	declined.recordStep("editors", StepDeclined, declined.mark())
	if slices.Contains(declined.PendingPillars(), "IDE Ecosystem") {
		t.Fatalf("declined pillar pending: %q", declined.PendingPillars())
	}
}

// End to end, the reproduction of #594: a Python project with pytest configured in
// pyproject.toml and a passing make test target. Adoption reports the pytest gate and the file it
// came from, names the test target, writes the failing pair once, and never marks the
// Verification Gate ready, on the first run or the next.
func TestAdoptPythonProjectWithoutBuildWarnsVerificationGate(t *testing.T) {
	repoPath := newTestRepo(t, "pytool")
	mustWrite(t, filepath.Join(repoPath, "pyproject.toml"), "[project]\nname = \"pytool\"\nversion = \"0.1.0\"\n\n[tool.pytest.ini_options]\ntestpaths = [\"tests\"]\n")
	mustWrite(t, filepath.Join(repoPath, "pytool.py"), "def add(a, b):\n    return a + b\n")
	mustWrite(t, filepath.Join(repoPath, "tests", "test_add.py"), "from pytool import add\n\n\ndef test_add():\n    assert add(1, 2) == 3\n")
	mustWrite(t, filepath.Join(repoPath, "Makefile"), ".PHONY: test\ntest:\n\tpython3 -m pytest\n")
	opts := AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath}
	for run := 1; run <= 2; run++ {
		rep, err := Adopt(context.Background(), opts)
		if err != nil {
			t.Fatalf("run %d: %v", run, err)
		}
		if rep.Verification.Status != verificationUnavailable || !planRunsPytest(rep.Verification) {
			t.Fatalf("run %d: plan = %+v", run, rep.Verification)
		}
		assertWarningContains(t, rep, "pyproject.toml [tool.pytest.ini_options]", "Makefile target 'test' already exists")
		pillar := pillarNamed(t, rep, "Verification Gate")
		if pillar.Status != PillarWarned || strings.HasPrefix(pillar.Line(), "✓") || !slices.Contains(rep.PendingPillars(), "Verification Gate") {
			t.Fatalf("run %d: Verification Gate = %+v", run, pillar)
		}
		makefile := mustRead(t, filepath.Join(repoPath, "Makefile"))
		if count := strings.Count(makefile, unavailableVerificationRecipe); count != 1 || !strings.Contains(makefile, "test:\n\tpython3 -m pytest\n") {
			t.Fatalf("run %d: Makefile holds the failing pair %d times:\n%s", run, count, makefile)
		}
	}
}

// assertWarningContains fails unless every want appears in some warning of rep.
func assertWarningContains(t *testing.T, rep *AdoptReport, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !slices.ContainsFunc(rep.Warnings, func(w string) bool { return strings.Contains(w, want) }) {
			t.Fatalf("no warning contains %q: %q", want, rep.Warnings)
		}
	}
}

// Re-run on the Makefile adoption generated itself: its test target is the failing placeholder,
// not the adopter's test contract, so no run names it, and the Verification Gate stays warned.
func TestAdoptRerunOnGeneratedMakefileDoesNotNameItsTestTarget(t *testing.T) {
	repoPath := newTestRepo(t, "pytool")
	mustWrite(t, filepath.Join(repoPath, "pyproject.toml"), "[project]\nname = \"pytool\"\nversion = \"0.1.0\"\n\n[tool.pytest.ini_options]\ntestpaths = [\"tests\"]\n")
	opts := AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath}
	for run := 1; run <= 2; run++ {
		rep, err := Adopt(context.Background(), opts)
		if err != nil {
			t.Fatalf("run %d: %v", run, err)
		}
		if slices.ContainsFunc(rep.Warnings, func(w string) bool { return strings.Contains(w, "Makefile target 'test' already exists") }) {
			t.Fatalf("run %d: generated placeholder named as the test contract: %q", run, rep.Warnings)
		}
		if pillar := pillarNamed(t, rep, "Verification Gate"); pillar.Status != PillarWarned {
			t.Fatalf("run %d: Verification Gate = %+v", run, pillar)
		}
		if makefile := mustRead(t, filepath.Join(repoPath, "Makefile")); !strings.Contains(makefile, "test: build\n"+unavailableVerificationRecipe) {
			t.Fatalf("run %d: Makefile is not the generated placeholder:\n%s", run, makefile)
		}
	}
}

// noteExistingTestTarget names only a test target the adopter wrote: one whose recipe is not the
// failing placeholder adoption renders, in a Makefile adoption does not replace, while the plan is
// unavailable.
func TestNoteExistingTestTargetNamesOnlyTheAdoptersTarget(t *testing.T) {
	unavailable := &VerificationPlan{Status: verificationUnavailable, Build: [][]string{}, Test: [][]string{pytestCommand}}
	declared := &VerificationPlan{Status: verificationDeclared, Build: [][]string{{"go", "build"}}, Test: [][]string{{"go", "test"}}}
	adopters := ".PHONY: test\ntest:\n\tpython3 -m pytest\n"
	appended, err := appendVerificationTargets(adopters, unavailable)
	if err != nil {
		t.Fatal(err)
	}
	generated := buildMakefile(unavailable)
	documented, err := mergeDocumentationMakefile(generated, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name              string
		data              string
		plan              *VerificationPlan
		exists, replaced  bool
		wantNamedTestRule bool
	}{
		{"adopters-target", adopters, unavailable, true, false, true},
		{"adopters-target-beside-appended-verify-all", appended, unavailable, true, false, true},
		{"adopters-target-crlf", strings.ReplaceAll(adopters, "\n", "\r\n"), unavailable, true, false, true},
		{"rule-without-recipe", "test: build\n\nbuild:\n\tpython3 -m build\n", unavailable, true, false, true},
		{"generated", generated, unavailable, true, false, false},
		{"generated-with-documentation-block", documented, unavailable, true, false, false},
		{"generated-crlf", strings.ReplaceAll(generated, "\n", "\r\n"), unavailable, true, false, false},
		{"generated-edited-elsewhere", generated + "\nlint:\n\truff check .\n", unavailable, true, false, false},
		{"no-test-target", "build:\n\tpython3 -m build\n", unavailable, true, false, false},
		{"declared-plan", adopters, declared, true, false, false},
		{"no-makefile", "", unavailable, false, false, false},
		{"replaced-earlier-output", adopters, unavailable, true, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &adoptSession{report: &AdoptReport{}, verification: tc.plan}
			noteExistingTestTarget(s, tc.data, tc.exists, tc.replaced)
			named := slices.ContainsFunc(s.report.Warnings, func(w string) bool { return strings.Contains(w, "Makefile target 'test' already exists") })
			if named != tc.wantNamedTestRule {
				t.Fatalf("named test target = %v, want %v: %q", named, tc.wantNamedTestRule, s.report.Warnings)
			}
		})
	}
}

// verificationTargetRecipe reads the recipe lines of the first rule for a target, stops at the
// first line that is not a recipe line, and finds no rule inside a define body.
func TestVerificationTargetRecipe(t *testing.T) {
	for _, tc := range []struct {
		name, data string
		want       string
		found      bool
	}{
		{"recipe", "test:\n\tpython3 -m pytest\n\tpython3 -m mypy\n\nbuild:\n\techo b\n", "\tpython3 -m pytest\n\tpython3 -m mypy\n", true},
		{"last-line-without-newline", "test: build\n\tpython3 -m pytest", "\tpython3 -m pytest\n", true},
		{"first-rule-wins", "test: build\ntest:\n\tpython3 -m pytest\n", "", true},
		{"absent", "build:\n\techo b\n", "", false},
		{"define-body", "define rules\ntest:\n\techo t\nendef\n", "", false},
		{"empty", "", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, found := verificationTargetRecipe(tc.data, "test")
			if got != tc.want || found != tc.found {
				t.Fatalf("verificationTargetRecipe = %q, %v; want %q, %v", got, found, tc.want, tc.found)
			}
		})
	}
}
