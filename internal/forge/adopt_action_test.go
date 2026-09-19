package forge

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/testsupport"
	"gopkg.in/yaml.v3"
)

const (
	// The composite action adopters are told to call (docs/adoption.md). Its steps run on a
	// runner with the caller's inputs in hand, so they are asserted here, not left to review.
	adoptActionPath  = ".github/actions/praetor-adopt/action.yml"
	adoptRunStepID   = "run-praetor"
	adoptBuildStepID = "build-standardsctl"
	adoptModulePath  = "github.com/cordanaLLM/praetor/cmd/standardsctl"
	maxOutputLines   = 4096
	adoptStepTimeout = 2 * time.Minute
)

// adoptStubSource stands in for the executables the steps call: it records the argument vector it
// was handed, so a test asserts what the script passed rather than what the script looks like,
// prints a line the report output must carry, and exits with a code the test chooses. Each name
// listed in PRAETOR_STUB_ENV is recorded after the arguments as NAME=value, so a caller can assert
// on the environment a step exported as well as on its argv.
const adoptStubSource = `package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

func main() {
	log, err := os.OpenFile(os.Getenv("PRAETOR_STUB_LOG"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		fmt.Fprintln(os.Stderr, "stub:", err)
		os.Exit(70)
	}
	fields := os.Args[1:]
	for _, name := range strings.Split(os.Getenv("PRAETOR_STUB_ENV"), ",") {
		if name != "" {
			fields = append(fields, name+"="+os.Getenv(name))
		}
	}
	if _, err := fmt.Fprintln(log, strings.Join(fields, "\t")); err != nil {
		fmt.Fprintln(os.Stderr, "stub:", err)
		os.Exit(70)
	}
	if err := log.Close(); err != nil {
		fmt.Fprintln(os.Stderr, "stub:", err)
		os.Exit(70)
	}
	fmt.Println("standardsctl-stub:", strings.Join(os.Args[1:], " "))
	code, err := strconv.Atoi(os.Getenv("PRAETOR_STUB_EXIT"))
	if err != nil {
		code = 0
	}
	os.Exit(code)
}
`

// compositeAction is the subset of an action definition these tests decide on.
type compositeAction struct {
	Inputs map[string]struct {
		Default string `yaml:"default"`
	} `yaml:"inputs"`
	Outputs map[string]struct {
		Value string `yaml:"value"`
	} `yaml:"outputs"`
	Runs struct {
		Using string          `yaml:"using"`
		Steps []compositeStep `yaml:"steps"`
	} `yaml:"runs"`
}

// compositeStep is one step of a composite action.
type compositeStep struct {
	Name string            `yaml:"name"`
	ID   string            `yaml:"id"`
	Env  map[string]string `yaml:"env"`
	Run  string            `yaml:"run"`
}

// loadAdoptAction parses this repository's own praetor-adopt action.
func loadAdoptAction(t *testing.T) compositeAction {
	t.Helper()
	path := filepath.Join(engineRoot, filepath.FromSlash(adoptActionPath))
	data, err := os.ReadFile(path) //nolint:gosec // a fixed path inside the repository under test
	if err != nil {
		t.Fatalf("read praetor-adopt action: %v", err)
	}
	var action compositeAction
	if err := yaml.Unmarshal(data, &action); err != nil {
		t.Fatalf("parse praetor-adopt action: %v", err)
	}
	if action.Runs.Using != "composite" || len(action.Runs.Steps) == 0 {
		t.Fatalf("praetor-adopt is not a composite action with steps: %+v", action.Runs)
	}
	return action
}

// adoptStep returns the step carrying the given id.
func adoptStep(t *testing.T, action compositeAction, id string) compositeStep {
	t.Helper()
	for i := 0; i < len(action.Runs.Steps) && i < maxJobsPerFile; i++ {
		if action.Runs.Steps[i].ID == id {
			return action.Runs.Steps[i]
		}
	}
	t.Fatalf("praetor-adopt has no step with id %q", id)
	return compositeStep{}
}

// TestPraetorAdoptAction_Negative_NoRunBodyCarriesAnExpression is the regression guard for the
// injection itself. GitHub substitutes an expression into the script text before bash parses it,
// so an input reaching a run body is executed rather than passed. No body may contain one.
func TestPraetorAdoptAction_Negative_NoRunBodyCarriesAnExpression(t *testing.T) {
	action := loadAdoptAction(t)
	for i := 0; i < len(action.Runs.Steps) && i < maxJobsPerFile; i++ {
		step := action.Runs.Steps[i]
		if step.Run == "" {
			continue
		}
		if strings.Contains(step.Run, "${{") {
			t.Errorf("step %q interpolates an expression into its shell body:\n%s", step.Name, step.Run)
		}
	}
}

// TestPraetorAdoptAction_Positive_EveryRuntimeInputArrivesThroughEnv pairs the guard above: the
// values still have to reach the script, and env is the only route left.
func TestPraetorAdoptAction_Positive_EveryRuntimeInputArrivesThroughEnv(t *testing.T) {
	run := adoptStep(t, loadAdoptAction(t), adoptRunStepID)
	for _, name := range []string{"path", "mode", "dry-run", "force", "record-baseline"} {
		variable := envVariableFor(run, "${{ inputs."+name+" }}")
		if variable == "" {
			t.Errorf("input %q reaches no environment variable of the run step: %v", name, run.Env)
			continue
		}
		if !strings.Contains(run.Run, variable) {
			t.Errorf("environment variable %q is declared but never read by the run step", variable)
		}
	}
	if envVariableFor(run, "${{ steps."+adoptBuildStepID+".outputs.binary }}") == "" {
		t.Errorf("the run step does not take its binary from the build step: %v", run.Env)
	}
}

// TestPraetorAdoptAction_Positive_DeclaredOutputNamesAStepThatWritesIt closes the second defect
// statically: outputs.report pointed at a step output nothing produced.
func TestPraetorAdoptAction_Positive_DeclaredOutputNamesAStepThatWritesIt(t *testing.T) {
	action := loadAdoptAction(t)
	declared, ok := action.Outputs["report"]
	if !ok {
		t.Fatal("praetor-adopt no longer declares a report output")
	}
	if declared.Value != "${{ steps."+adoptRunStepID+".outputs.report }}" {
		t.Fatalf("report output reads an unexpected step: %q", declared.Value)
	}
	body := adoptStep(t, action, adoptRunStepID).Run
	if !strings.Contains(body, `"$GITHUB_OUTPUT"`) || !strings.Contains(body, "report<<") {
		t.Errorf("the run step never writes a report to GITHUB_OUTPUT:\n%s", body)
	}
	if !strings.Contains(adoptStep(t, action, adoptBuildStepID).Run, "binary=") {
		t.Error("the build step never writes the binary output the run step consumes")
	}
}

// envVariableFor returns the name of the step variable carrying expression, or "".
func envVariableFor(step compositeStep, expression string) string {
	for key := range step.Env {
		if step.Env[key] == expression {
			return key
		}
	}
	return ""
}

// stepOutcome is what one execution of a step produced.
type stepOutcome struct {
	exitCode    int
	outputs     map[string]string
	invocations [][]string
	workDir     string
	runnerTemp  string
	combined    string
}

// adoptShell resolves the interpreter the action declares, or skips with the reason.
func adoptShell(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the action declares shell: bash; a Windows host has no bash to execute the body with")
	}
	shell, err := exec.LookPath("bash")
	if err != nil {
		t.Skipf("bash is not available to execute the action's shell body: %v", err)
	}
	return shell
}

// executeAdoptBody runs one step's own shell body with a stub for the executable it calls, a
// private GITHUB_OUTPUT and the extra environment the caller supplies, so every assertion is about
// the shipped script rather than about a copy of it.
func executeAdoptBody(t *testing.T, stepID, stub string, extra func(binDir, temp string) []string) stepOutcome {
	t.Helper()
	shell := adoptShell(t)
	body := adoptStep(t, loadAdoptAction(t), stepID).Run
	work, binDir, temp := t.TempDir(), t.TempDir(), t.TempDir()
	log := filepath.Join(t.TempDir(), "calls.log")
	outputFile := filepath.Join(t.TempDir(), "github_output")
	testsupport.BuildExecutable(t, binDir, stub, adoptStubSource)
	env := []string{
		"PATH=" + binDir + string(os.PathListSeparator) + os.Getenv("PATH"),
		"HOME=" + t.TempDir(), "RUNNER_TEMP=" + temp,
		"GITHUB_OUTPUT=" + outputFile, "PRAETOR_STUB_LOG=" + log,
	}
	ctx, cancel := context.WithTimeout(t.Context(), adoptStepTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, shell, "-c", body)
	cmd.Dir = work
	cmd.Env = append(env, extra(binDir, temp)...)
	combined, runErr := cmd.CombinedOutput()
	return stepOutcome{
		exitCode: exitCodeOf(t, runErr), outputs: parseStepOutputs(t, outputFile),
		invocations: readInvocations(t, log), workDir: work, runnerTemp: temp,
		combined: string(combined),
	}
}

// runAdoptStep executes the run step's own shell body with the supplied input values.
func runAdoptStep(t *testing.T, values map[string]string) stepOutcome {
	t.Helper()
	return executeAdoptBody(t, adoptRunStepID, "standardsctl",
		func(binDir, _ string) []string { return adoptStepEnv(t, values, binDir) })
}

// runAdoptBuildStep executes the build step's own shell body with the action checked out at
// actionPath and the caller's ref in hand.
func runAdoptBuildStep(t *testing.T, actionPath, ref string) stepOutcome {
	t.Helper()
	return executeAdoptBody(t, adoptBuildStepID, "go", func(_, _ string) []string {
		return []string{
			"PRAETOR_STUB_ENV=GOBIN", "PRAETOR_STUB_EXIT=0",
			"GITHUB_ACTION_PATH=" + actionPath, "PRAETOR_ACTION_REF=" + ref,
		}
	})
}

// adoptStepEnv builds the environment GitHub would hand the run step, defaulting every input the
// caller did not override to the default the action file itself declares.
func adoptStepEnv(t *testing.T, values map[string]string, binDir string) []string {
	t.Helper()
	declared := adoptRunDefaults(t)
	declared["PRAETOR_BIN"] = filepath.Join(binDir, testsupport.ExecutableName("standardsctl"))
	declared["PRAETOR_STUB_EXIT"] = "0"
	for key := range values {
		if _, ok := declared[key]; !ok {
			t.Fatalf("test sets %q, which the action does not declare", key)
		}
		declared[key] = values[key]
	}
	env := make([]string, 0, len(declared))
	for key := range declared {
		env = append(env, key+"="+declared[key])
	}
	return env
}

// adoptRunDefaults reads the run step's env block and pairs every variable it maps from an input
// with that input's declared default, so a default changed in action.yml changes what these tests
// assert instead of being re-certified by a copy of it kept here.
func adoptRunDefaults(t *testing.T) map[string]string {
	t.Helper()
	action := loadAdoptAction(t)
	run := adoptStep(t, action, adoptRunStepID)
	defaults := make(map[string]string, len(run.Env))
	for key := range run.Env {
		name, mapped := strings.CutPrefix(run.Env[key], "${{ inputs.")
		if !mapped {
			continue
		}
		name = strings.TrimSuffix(name, " }}")
		input, declared := action.Inputs[name]
		if !declared {
			t.Fatalf("the run step maps input %q, which the action does not declare", name)
		}
		defaults[key] = input.Default
	}
	if len(defaults) == 0 {
		t.Fatal("the run step maps no input into its environment")
	}
	return defaults
}

// exitCodeOf maps a command error onto the exit code the step reported.
func exitCodeOf(t *testing.T, err error) int {
	t.Helper()
	if err == nil {
		return 0
	}
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		t.Fatalf("run step could not be executed: %v", err)
	}
	return exit.ExitCode()
}

// parseStepOutputs reads a GITHUB_OUTPUT file, including the multiline heredoc form.
func parseStepOutputs(t *testing.T, path string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(path) //nolint:gosec // the path is this test's own temporary file
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}
		}
		t.Fatalf("read GITHUB_OUTPUT: %v", err)
	}
	if len(data) == 0 {
		return map[string]string{}
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(lines) > maxOutputLines {
		t.Fatalf("GITHUB_OUTPUT carries %d lines, beyond the %d this test reads", len(lines), maxOutputLines)
	}
	return collectOutputs(lines)
}

// collectOutputs folds GITHUB_OUTPUT lines into name/value pairs.
func collectOutputs(lines []string) map[string]string {
	outputs := make(map[string]string, len(lines))
	for i := 0; i < len(lines); i++ {
		name, delimiter, heredoc := strings.Cut(lines[i], "<<")
		if !heredoc {
			if key, value, ok := strings.Cut(lines[i], "="); ok {
				outputs[key] = value
			}
			continue
		}
		body := make([]string, 0, len(lines))
		for i+1 < len(lines) && lines[i+1] != delimiter {
			i++
			body = append(body, lines[i])
		}
		i++
		outputs[name] = strings.Join(body, "\n")
	}
	return outputs
}

// readInvocations returns one argument vector per call the stub recorded.
func readInvocations(t *testing.T, path string) [][]string {
	t.Helper()
	data, err := os.ReadFile(path) //nolint:gosec // the path is this test's own temporary file
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("read stub log: %v", err)
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	calls := make([][]string, 0, len(lines))
	for i := 0; i < len(lines) && i < maxOutputLines; i++ {
		if lines[i] != "" {
			calls = append(calls, strings.Split(lines[i], "\t"))
		}
	}
	return calls
}

// TestPraetorAdoptAction_Positive_AdoptRunWritesANonEmptyReport is the case the declared output
// promised and never delivered. The argv it expects is the one the action's own declared defaults
// produce, so a default edited in action.yml fails here rather than being certified unchanged.
func TestPraetorAdoptAction_Positive_AdoptRunWritesANonEmptyReport(t *testing.T) {
	got := runAdoptStep(t, nil)
	if got.exitCode != 0 {
		t.Fatalf("a successful run exited %d:\n%s", got.exitCode, got.combined)
	}
	want := [][]string{
		{"adopt", "--path=.", "--dry-run=false", "--force=false", "--record-baseline=true"},
		{"compile-context", "--verify"},
	}
	if !equalInvocations(got.invocations, want) {
		t.Errorf("run step called %v, want %v", got.invocations, want)
	}
	report := got.outputs["report"]
	if report == "" {
		t.Fatalf("report output is empty; GITHUB_OUTPUT held %v", got.outputs)
	}
	if !strings.Contains(report, "standardsctl-stub: adopt --path=.") {
		t.Errorf("report output does not carry the run's own output:\n%s", report)
	}
}

// TestPraetorAdoptAction_Negative_BooleanOptOutsReachTheCommand pins the opt-out half of every
// boolean input. dogfood's --dry-run (cmd/standardsctl/dogfood.go) and adopt's --record-baseline
// (cmd/standardsctl/adopt.go) default to true inside standardsctl, so a flag merely left off the
// argv is an opt-in: each value has to be passed explicitly to mean anything.
func TestPraetorAdoptAction_Negative_BooleanOptOutsReachTheCommand(t *testing.T) {
	cases := map[string]struct {
		values map[string]string
		want   []string
	}{
		"dogfood dry-run off": {
			values: map[string]string{"PRAETOR_MODE": "dogfood", "PRAETOR_DRY_RUN": "false"},
			want:   []string{"dogfood", "--path=.", "--dry-run=false"},
		},
		"record-baseline off": {
			values: map[string]string{"PRAETOR_RECORD_BASELINE": "false"},
			want:   []string{"adopt", "--path=.", "--dry-run=false", "--force=false", "--record-baseline=false"},
		},
		"force on": {
			values: map[string]string{"PRAETOR_FORCE": "true"},
			want:   []string{"adopt", "--path=.", "--dry-run=false", "--force=true", "--record-baseline=true"},
		},
	}
	for name := range cases {
		t.Run(name, func(t *testing.T) {
			got := runAdoptStep(t, cases[name].values)
			if got.exitCode != 0 {
				t.Fatalf("run step exited %d:\n%s", got.exitCode, got.combined)
			}
			if !equalInvocations(got.invocations[:min(len(got.invocations), 1)], [][]string{cases[name].want}) {
				t.Errorf("run step called %v, want %v first", got.invocations, cases[name].want)
			}
		})
	}
}

// TestPraetorAdoptAction_Negative_RejectedInputsRunNothingButStillReport covers every value the
// step refuses. An unrecognised mode used to fall through to the mutating adopt branch, so a
// caller's typo wrote .standards.yaml into a repository that asked for a read-only benchmark.
func TestPraetorAdoptAction_Negative_RejectedInputsRunNothingButStillReport(t *testing.T) {
	cases := map[string]struct {
		values  map[string]string
		message string
	}{
		"unknown mode":      {map[string]string{"PRAETOR_MODE": "Dogfood"}, "takes adopt or dogfood"},
		"non-boolean force": {map[string]string{"PRAETOR_FORCE": "yes"}, "'force' input takes true or false"},
		"empty path":        {map[string]string{"PRAETOR_PATH": ""}, "must not be empty"},
	}
	for name := range cases {
		t.Run(name, func(t *testing.T) {
			got := runAdoptStep(t, cases[name].values)
			if got.exitCode == 0 {
				t.Fatalf("the step accepted the input:\n%s", got.combined)
			}
			if len(got.invocations) != 0 {
				t.Errorf("a rejected input still ran %v", got.invocations)
			}
			if !strings.Contains(got.combined, cases[name].message) {
				t.Errorf("the failure does not name the input:\n%s", got.combined)
			}
			if !strings.Contains(got.outputs["report"], cases[name].message) {
				t.Errorf("the report output lost the rejection: %q", got.outputs["report"])
			}
		})
	}
}

// TestPraetorAdoptAction_Negative_FailingRunStillPublishesItsReport keeps the report from being
// lost exactly when a consumer needs it, and keeps the failure visible.
func TestPraetorAdoptAction_Negative_FailingRunStillPublishesItsReport(t *testing.T) {
	got := runAdoptStep(t, map[string]string{"PRAETOR_STUB_EXIT": "3"})
	if got.exitCode != 3 {
		t.Fatalf("the step hid the command's exit status: got %d, want 3\n%s", got.exitCode, got.combined)
	}
	if len(got.invocations) != 1 {
		t.Errorf("a failed adopt must not be followed by compile-context: %v", got.invocations)
	}
	if !strings.Contains(got.outputs["report"], "standardsctl-stub: adopt") {
		t.Errorf("report output lost the failing run: %q", got.outputs["report"])
	}
}

// TestPraetorAdoptAction_Negative_ShellMetacharactersInPathStayOneArgument is the defect's own
// payload: a path that used to become script text now reaches the binary as data.
func TestPraetorAdoptAction_Negative_ShellMetacharactersInPathStayOneArgument(t *testing.T) {
	payload := `.";touch pwned;echo "`
	got := runAdoptStep(t, map[string]string{"PRAETOR_PATH": payload})
	if got.exitCode != 0 {
		t.Fatalf("run step exited %d:\n%s", got.exitCode, got.combined)
	}
	want := [][]string{
		{"adopt", "--path=" + payload, "--dry-run=false", "--force=false", "--record-baseline=true"},
		{"compile-context", "--verify"},
	}
	if !equalInvocations(got.invocations, want) {
		t.Errorf("run step called %v, want %v", got.invocations, want)
	}
	if _, err := os.Stat(filepath.Join(got.workDir, "pwned")); err == nil {
		t.Fatal("the path input ran a command of its own")
	}
}

// TestPraetorAdoptAction_Boundary_EmptyBinaryFallsBackToPath covers the one value the build step
// cannot supply: with no binary output in hand the step has to resolve standardsctl from PATH.
func TestPraetorAdoptAction_Boundary_EmptyBinaryFallsBackToPath(t *testing.T) {
	got := runAdoptStep(t, map[string]string{"PRAETOR_BIN": ""})
	if got.exitCode != 0 {
		t.Fatalf("run step exited %d:\n%s", got.exitCode, got.combined)
	}
	if len(got.invocations) != 2 {
		t.Errorf("the PATH fallback did not run the command: %v", got.invocations)
	}
}

// TestPraetorAdoptAction_Boundary_DogfoodDryRunSkipsCompileContext covers the other mode, where
// both the flag set and the follow-up command differ.
func TestPraetorAdoptAction_Boundary_DogfoodDryRunSkipsCompileContext(t *testing.T) {
	got := runAdoptStep(t, map[string]string{
		"PRAETOR_MODE": "dogfood", "PRAETOR_DRY_RUN": "true", "PRAETOR_PATH": "repo",
	})
	if got.exitCode != 0 {
		t.Fatalf("run step exited %d:\n%s", got.exitCode, got.combined)
	}
	want := [][]string{{"dogfood", "--path=repo", "--dry-run=true"}}
	if !equalInvocations(got.invocations, want) {
		t.Errorf("run step called %v, want %v", got.invocations, want)
	}
	if got.outputs["report"] == "" {
		t.Error("dogfood mode wrote no report output")
	}
}

// adoptCheckoutFixture writes the directory layout GitHub creates for this action and returns the
// action's own path inside it together with the checkout root. withSource decides whether the
// praetor module sits above the action, which is what separates a pinned checkout of this
// repository from a copy of the action vendored somewhere else.
func adoptCheckoutFixture(t *testing.T, withSource bool) (actionPath, root string) {
	t.Helper()
	root = t.TempDir()
	actionPath = filepath.Join(root, ".github", "actions", "praetor-adopt")
	if err := os.MkdirAll(actionPath, 0o750); err != nil {
		t.Fatalf("create action directory: %v", err)
	}
	if !withSource {
		return actionPath, root
	}
	if err := os.MkdirAll(filepath.Join(root, "cmd", "standardsctl"), 0o750); err != nil {
		t.Fatalf("create command directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.test\n"), 0o600); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	return actionPath, root
}

// TestPraetorAdoptBuild_Positive_BuildsTheCheckoutTheCallerPinned is the pin itself: the binary is
// built from the praetor checkout the action was resolved from, so praetor-adopt@<ref> runs the
// standardsctl of that ref rather than whatever the module proxy currently calls latest.
func TestPraetorAdoptBuild_Positive_BuildsTheCheckoutTheCallerPinned(t *testing.T) {
	actionPath, root := adoptCheckoutFixture(t, true)
	got := runAdoptBuildStep(t, actionPath, "v1.2.3")
	if got.exitCode != 0 {
		t.Fatalf("build step exited %d:\n%s", got.exitCode, got.combined)
	}
	if len(got.invocations) != 1 || len(got.invocations[0]) != 7 {
		t.Fatalf("build step ran go %v, want one build call", got.invocations)
	}
	binary := filepath.Join(got.runnerTemp, "standardsctl")
	call := got.invocations[0]
	call[2] = filepath.Clean(call[2])
	want := []string{"build", "-C", root, "-o", binary, "./cmd/standardsctl", "GOBIN="}
	if !equalInvocations([][]string{call}, [][]string{want}) {
		t.Errorf("build step ran go %v, want %v", call, want)
	}
	if got.outputs["binary"] != binary {
		t.Errorf("binary output is %q, want %q", got.outputs["binary"], binary)
	}
}

// TestPraetorAdoptBuild_Boundary_VendoredCopyInstallsTheSameRef covers the layout where the action
// does not sit in a praetor checkout: the install still names the caller's ref, never @latest.
func TestPraetorAdoptBuild_Boundary_VendoredCopyInstallsTheSameRef(t *testing.T) {
	actionPath, _ := adoptCheckoutFixture(t, false)
	got := runAdoptBuildStep(t, actionPath, "v1.2.3")
	if got.exitCode != 0 {
		t.Fatalf("build step exited %d:\n%s", got.exitCode, got.combined)
	}
	want := [][]string{{"install", adoptModulePath + "@v1.2.3", "GOBIN=" + got.runnerTemp}}
	if !equalInvocations(got.invocations, want) {
		t.Errorf("build step ran go %v, want %v", got.invocations, want)
	}
	if got.outputs["binary"] != filepath.Join(got.runnerTemp, "standardsctl") {
		t.Errorf("binary output is %q, want the installed path", got.outputs["binary"])
	}
	if strings.Contains(adoptStep(t, loadAdoptAction(t), adoptBuildStepID).Run, "@latest") {
		t.Error("the build step still installs an unpinned @latest")
	}
}

// TestPraetorAdoptBuild_Negative_NoSourceAndNoRefRunsNothing is the case that must not quietly
// resolve to some other praetor: with neither a checkout nor a ref there is nothing to pin to.
func TestPraetorAdoptBuild_Negative_NoSourceAndNoRefRunsNothing(t *testing.T) {
	actionPath, _ := adoptCheckoutFixture(t, false)
	got := runAdoptBuildStep(t, actionPath, "")
	if got.exitCode == 0 {
		t.Fatalf("the build step resolved a binary out of nothing:\n%s", got.combined)
	}
	if len(got.invocations) != 0 {
		t.Errorf("the build step still ran go %v", got.invocations)
	}
	if _, published := got.outputs["binary"]; published {
		t.Errorf("a failed build still published a binary output: %v", got.outputs)
	}
	if !strings.Contains(got.combined, "no praetor source") {
		t.Errorf("the failure does not say what is missing:\n%s", got.combined)
	}
}

// equalInvocations compares two recorded argument-vector sequences.
func equalInvocations(got, want [][]string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := 0; i < len(got) && i < maxOutputLines; i++ {
		if strings.Join(got[i], "\t") != strings.Join(want[i], "\t") {
			return false
		}
	}
	return true
}
