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
	maxOutputLines   = 4096
	adoptStepTimeout = 2 * time.Minute
)

// adoptStubSource stands in for standardsctl: it records the argument vector it was handed, so a
// test asserts what the script passed rather than what the script looks like, prints a line the
// report output must carry, and exits with a code the test chooses.
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
	if _, err := fmt.Fprintln(log, strings.Join(os.Args[1:], "\t")); err != nil {
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

// stepOutcome is what one execution of the run step produced.
type stepOutcome struct {
	exitCode    int
	outputs     map[string]string
	invocations [][]string
	workDir     string
	combined    string
}

// runAdoptStep executes the run step's own shell body with the supplied input values, a stub
// standardsctl on PATH and a private GITHUB_OUTPUT, so the assertions are about the shipped script
// rather than about a copy of it.
func runAdoptStep(t *testing.T, values map[string]string) stepOutcome {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the action declares shell: bash; a Windows host has no bash to execute the body with")
	}
	shell, err := exec.LookPath("bash")
	if err != nil {
		t.Skipf("bash is not available to execute the action's shell body: %v", err)
	}
	body := adoptStep(t, loadAdoptAction(t), adoptRunStepID).Run
	work, binDir := t.TempDir(), t.TempDir()
	log := filepath.Join(t.TempDir(), "calls.log")
	outputFile := filepath.Join(t.TempDir(), "github_output")
	testsupport.BuildExecutable(t, binDir, "standardsctl", adoptStubSource)
	ctx, cancel := context.WithTimeout(t.Context(), adoptStepTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, shell, "-c", body)
	cmd.Dir = work
	cmd.Env = adoptStepEnv(t, values, binDir, log, outputFile)
	combined, runErr := cmd.CombinedOutput()
	return stepOutcome{
		exitCode:    exitCodeOf(t, runErr),
		outputs:     parseStepOutputs(t, outputFile),
		invocations: readInvocations(t, log),
		workDir:     work,
		combined:    string(combined),
	}
}

// adoptStepEnv builds the environment GitHub would hand the step, defaulting every input the
// caller did not override to the action's own declared default.
func adoptStepEnv(t *testing.T, values map[string]string, binDir, log, outputFile string) []string {
	t.Helper()
	declared := map[string]string{
		"PRAETOR_BIN": "standardsctl", "PRAETOR_PATH": ".", "PRAETOR_MODE": "adopt",
		"PRAETOR_DRY_RUN": "false", "PRAETOR_FORCE": "false", "PRAETOR_RECORD_BASELINE": "true",
		"PRAETOR_STUB_EXIT": "0",
	}
	for key := range values {
		if _, ok := declared[key]; !ok {
			t.Fatalf("test sets %q, which the action does not declare", key)
		}
		declared[key] = values[key]
	}
	env := []string{
		"PATH=" + binDir + string(os.PathListSeparator) + os.Getenv("PATH"),
		"HOME=" + t.TempDir(), "RUNNER_TEMP=" + t.TempDir(),
		"GITHUB_OUTPUT=" + outputFile, "PRAETOR_STUB_LOG=" + log,
	}
	for key := range declared {
		env = append(env, key+"="+declared[key])
	}
	return env
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
// promised and never delivered.
func TestPraetorAdoptAction_Positive_AdoptRunWritesANonEmptyReport(t *testing.T) {
	got := runAdoptStep(t, nil)
	if got.exitCode != 0 {
		t.Fatalf("a successful run exited %d:\n%s", got.exitCode, got.combined)
	}
	want := [][]string{
		{"adopt", "--path=.", "--record-baseline"},
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
		{"adopt", "--path=" + payload, "--record-baseline"},
		{"compile-context", "--verify"},
	}
	if !equalInvocations(got.invocations, want) {
		t.Errorf("run step called %v, want %v", got.invocations, want)
	}
	if _, err := os.Stat(filepath.Join(got.workDir, "pwned")); err == nil {
		t.Fatal("the path input ran a command of its own")
	}
}

// TestPraetorAdoptAction_Boundary_EmptyPathRunsNothing covers the empty value a caller can pass
// explicitly even though the input declares a default.
func TestPraetorAdoptAction_Boundary_EmptyPathRunsNothing(t *testing.T) {
	got := runAdoptStep(t, map[string]string{"PRAETOR_PATH": ""})
	if got.exitCode == 0 {
		t.Fatalf("an empty path was accepted:\n%s", got.combined)
	}
	if len(got.invocations) != 0 {
		t.Errorf("an empty path still ran %v", got.invocations)
	}
	if !strings.Contains(got.combined, "must not be empty") {
		t.Errorf("the failure does not name the input:\n%s", got.combined)
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
	want := [][]string{{"dogfood", "--path=repo", "--dry-run"}}
	if !equalInvocations(got.invocations, want) {
		t.Errorf("run step called %v, want %v", got.invocations, want)
	}
	if got.outputs["report"] == "" {
		t.Error("dogfood mode wrote no report output")
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
