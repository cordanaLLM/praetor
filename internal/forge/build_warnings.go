package forge

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/ghworkflow"
	"gopkg.in/yaml.v3"
)

// The toolchains a build lane is named by (BuildLane.Toolchain).
const (
	ToolchainGCCClang = "gcc/clang"
	ToolchainMSVC     = "MSVC cl"
	ToolchainCMake    = "CMake"
	ToolchainMeson    = "Meson"
	ToolchainCargo    = "Cargo"
	ToolchainRustc    = "rustc"
	ToolchainGo       = "Go"
)

// buildWarningsTimeout bounds one MeasureBuildWarnings call (HISS-02).
const buildWarningsTimeout = 30 * time.Second

// BuildLane is one command of a workflow's run: steps that compiles C, C++, Rust or Go code, and
// whether a compiler warning fails it (HISS-10).
type BuildLane struct {
	// Workflow is the workflow's file name directly under .github/workflows.
	Workflow string
	// Job is the job's key under jobs:, and Step the step's label: its one-line command, else its
	// name, else the first line of its script (WorkflowRuns).
	Job, Step string
	// Line is the document line the step's run: starts on.
	Line int
	// Toolchain is one of the Toolchain constants.
	Toolchain string
	// Fatal reports that a warning fails the step. Detail names the form that makes it fatal, or
	// what the step lacks; Remedy is the form the toolchain documents.
	Fatal          bool
	Detail, Remedy string
}

// Where locates the lane: "<workflow>:<line>: job <job>, step <step>".
func (l BuildLane) Where() string {
	return fmt.Sprintf("%s/%s:%d: job %s, step %q", ghworkflow.Dir, l.Workflow, l.Line, l.Job, l.Step)
}

// BuildWarningsMeasurement is every build lane the workflow files hold, in workflow name order,
// job ID order, and step and command order.
type BuildWarningsMeasurement struct {
	Lanes []BuildLane
}

// MeasureBuildWarnings reads the run: steps of every workflow under repoPath's .github/workflows
// and names each command that compiles C, C++, Rust or Go code, with whether a warning fails it:
//
//   - gcc, clang and their cross and versioned names: -Werror on the command, not undone by a
//     later -Wno-error. -Werror=<warning> makes that one warning an error and does not count.
//   - MSVC cl and clang-cl: /WX (or -WX) on the command or in the CL or _CL_ variable, not undone
//     by a later /WX-.
//   - CMake: a configure run with CMAKE_COMPILE_WARNING_AS_ERROR set to a CMake true constant and
//     without --compile-no-warning-as-error, or with -Werror (or /WX) in every CMAKE_C_FLAGS and
//     CMAKE_CXX_FLAGS it defines. cmake --build in a job no run: step configures is a lane too:
//     what configured its tree cannot be read.
//   - Meson: meson setup with --werror or -Dwerror=true; meson compile, test or install in a job
//     no meson setup step precedes is a lane as cmake --build is.
//   - Cargo: build, check, test, run, bench, clippy, rustc and nextest with -D warnings (or
//     -Dwarnings, --deny, -F, --forbid) in CARGO_ENCODED_RUSTFLAGS, else in RUSTFLAGS, the
//     variable cargo reads first, or for clippy and rustc after their "--". rustc directly: on
//     its command.
//   - Go: go build, test, install and run (a module@version install or run builds a tool, not
//     the repository, and is no lane). The Go compiler reports no warnings; go vet does, so every
//     Go lane is fatal when some step of some workflow runs go vet or go test -vet=all.
//
// A variable is read from the command's own assignments, an export earlier in its script, then
// the step's, job's and workflow's env:. A step whose failure does not bind (continue-on-error on
// it or its job) is never fatal, and a job or step whose if: is the literal false is not read.
// What a make target, a script, an action, a preset or a build file sets is not read.
func MeasureBuildWarnings(ctx context.Context, repoPath string) (BuildWarningsMeasurement, error) {
	if ctx == nil {
		return BuildWarningsMeasurement{}, errors.New("build warnings measurement requires a context")
	}
	ctx, cancel := context.WithTimeout(ctx, buildWarningsTimeout)
	defer cancel()
	files, err := readWorkflowFiles(ctx, repoPath)
	if err != nil {
		return BuildWarningsMeasurement{}, err
	}
	measure := buildMeasure{configured: map[string]bool{}}
	for i := 0; i < len(files) && i < maxWorkflowFiles; i++ {
		if err := measure.workflow(files[i].Name, files[i].Data); err != nil {
			return BuildWarningsMeasurement{}, fmt.Errorf("workflow %s: %w", files[i].Name, err)
		}
	}
	return BuildWarningsMeasurement{Lanes: measure.result()}, nil
}

// buildMeasure collects the lanes of every workflow. goLanes indexes the Go lanes, judged once
// every workflow is read (result); vet is the first binding step that runs go vet; configured
// holds the jobs ("<workflow>/<job>/<toolchain>") a configure step of CMake or Meson ran in.
type buildMeasure struct {
	lanes      []BuildLane
	goLanes    []int
	vet        string
	configured map[string]bool
}

// workflow reads the run: steps of one workflow document.
func (m *buildMeasure) workflow(name string, data []byte) error {
	spec, err := ghworkflow.Parse(data)
	if err != nil {
		return err
	}
	steps, err := ghworkflow.RunSteps(&spec)
	if err != nil {
		return err
	}
	for i := 0; i < len(steps) && i < maxJobsPerFile*maxStepsPerJob; i++ {
		if err := m.step(name, &spec, steps[i]); err != nil {
			return fmt.Errorf("job %s, step %d: %w", steps[i].JobID, steps[i].Index+1, err)
		}
	}
	return nil
}

// laneSite is the step a command runs in, and the environment it reads.
type laneSite struct {
	lane     BuildLane
	advisory bool
	env      stepEnvironment
}

// step reads every command of one run: step.
func (m *buildMeasure) step(workflow string, spec *workflowSpec, run ghworkflow.RunStep) error {
	if ghworkflow.NeverRuns(run.Job.If) || ghworkflow.NeverRuns(run.Step.If) {
		return nil
	}
	fields, err := scriptFields(run.Step.Run)
	if err != nil {
		return err
	}
	label, _ := stepRun(*run.Step)
	site := laneSite{
		lane:     BuildLane{Workflow: workflow, Job: run.JobID, Step: label.Label, Line: run.Step.RunLine},
		advisory: advisoryJob(run.Job.ContinueOnError) || advisoryJob(run.Step.ContinueOnError),
		env:      stepEnvironment{scopes: []*yaml.Node{&run.Step.Env, &run.Job.Env, &spec.Env}, script: map[string]string{}},
	}
	commands := scriptCommands(fields)
	for i := 0; i < len(commands) && i < maxRunScriptFields; i++ {
		m.command(&site, parseShellCommand(commands[i]))
	}
	return nil
}

// command records the lane one command is, if any.
func (m *buildMeasure) command(site *laneSite, cmd shellCommand) {
	switch cmd.program {
	case "":
		site.env.assign(cmd.assigns, false)
		return
	case "export":
		site.env.assign(parseShellCommand(cmd.args).assigns, true)
		return
	}
	args := site.env.expand(cmd.args)
	if cmd.program == "go" {
		m.goCommand(site, args)
		return
	}
	verdict, ok := m.judge(site, cmd.program, args, cmd.assigns)
	if !ok {
		return
	}
	m.record(site, verdict)
}

// laneVerdict is what one judge decided about a command.
type laneVerdict struct {
	toolchain string
	fatal     bool
	detail    string
}

// judge decides the command of program with args under its prefix assignments assigns, and
// reports false for a command that compiles nothing this measurement reads.
func (m *buildMeasure) judge(site *laneSite, program string, args []string, assigns map[string]string) (laneVerdict, bool) {
	lookup := func(name string) (string, bool, bool) { return site.env.value(name, assigns) }
	switch {
	case cCompilerName.MatchString(program):
		return judgeCCompiler(args)
	case program == "cl" || program == "clang-cl":
		return judgeMSVC(args, lookup)
	case program == "cmake":
		return m.judgeCMake(site, args)
	case program == "meson":
		return m.judgeMeson(site, args)
	case program == "cargo":
		return judgeCargo(args, lookup)
	case program == "rustc":
		return judgeRustc(args)
	}
	return laneVerdict{}, false
}

// record appends the lane of a verdict at site. A step whose failure does not bind is never fatal.
func (m *buildMeasure) record(site *laneSite, verdict laneVerdict) {
	lane := site.lane
	lane.Toolchain, lane.Fatal, lane.Detail = verdict.toolchain, verdict.fatal, verdict.detail
	lane.Remedy = buildRemedies[verdict.toolchain]
	if site.advisory {
		lane.Fatal = false
		lane.Detail = "continue-on-error lets the step pass whatever its compiler reports (" + verdict.detail + ")"
	}
	m.lanes = append(m.lanes, lane)
}

// result judges every Go lane against the first go vet step, now that every workflow is read.
func (m *buildMeasure) result() []BuildLane {
	for i := 0; i < len(m.goLanes) && i < len(m.lanes); i++ {
		lane := &m.lanes[m.goLanes[i]]
		if m.vet == "" {
			lane.Detail = "no binding step of any workflow runs go vet or go test -vet=all"
			continue
		}
		lane.Fatal, lane.Detail = true, "go vet runs at "+m.vet
	}
	return m.lanes
}

// buildRemedies is the warnings-as-errors form each toolchain documents.
var buildRemedies = map[string]string{
	ToolchainGCCClang: "add -Werror to the command (gcc and clang: turn all warnings into errors; -Werror=<warning> covers that warning only)",
	ToolchainMSVC:     "add /WX to the command or to the CL environment variable (treat all compiler warnings as errors)",
	ToolchainCMake:    "configure with -DCMAKE_COMPILE_WARNING_AS_ERROR=ON (CMake 3.24 or later) and without --compile-no-warning-as-error",
	ToolchainMeson:    "set the build directory up with meson setup --werror or -Dwerror=true",
	ToolchainCargo:    `set RUSTFLAGS: "-D warnings" in the step's, job's or workflow's env:, or pass -- -D warnings to cargo clippy`,
	ToolchainRustc:    "pass -D warnings to rustc",
	ToolchainGo:       "run go vet ./... (or go test -vet=all) in a workflow step; the Go compiler itself reports no warnings",
}

// cCompilerName matches the GCC and Clang drivers by base name, lower-cased and without .exe:
// gcc, g++, cc, c++, clang, clang++ and the clang-based Intel icx and icpx, with a target prefix
// such as x86_64-w64-mingw32- and a version suffix such as -14.
var cCompilerName = regexp.MustCompile(`^(?:[a-z0-9_.]+-)*(?:gcc|g\+\+|cc|c\+\+|clang|clang\+\+|icx|icpx)(?:-[0-9][0-9.]*)?$`)

// cSourceSuffixes are the file suffixes, lower-cased, the drivers compile as C, C++ or
// Objective-C.
var cSourceSuffixes = [...]string{".c", ".cc", ".cpp", ".cxx", ".c++", ".m", ".mm"}

// judgeCCompiler decides a gcc or clang command: a lane when it compiles a source file or runs
// with -c, fatal when -Werror is in force at its end.
func judgeCCompiler(args []string) (laneVerdict, bool) {
	if !compilesSource(args, "-c") {
		return laneVerdict{}, false
	}
	if lastSwitch(args, []string{"-Werror"}, []string{"-Wno-error"}) {
		return laneVerdict{toolchain: ToolchainGCCClang, fatal: true, detail: "-Werror is on the command"}, true
	}
	return laneVerdict{toolchain: ToolchainGCCClang, detail: "the command carries no -Werror"}, true
}

// judgeMSVC decides a cl or clang-cl command: CL is prepended to its arguments and _CL_
// appended, and the last /WX or /WX- decides.
func judgeMSVC(args []string, lookup func(string) (string, bool, bool)) (laneVerdict, bool) {
	if !compilesSource(args, "/c", "-c") {
		return laneVerdict{}, false
	}
	prepended, _, _ := lookup("CL")
	appended, _, _ := lookup("_CL_")
	all := append(append(strings.Fields(prepended), args...), strings.Fields(appended)...)
	if lastSwitch(all, []string{"/WX", "-WX"}, []string{"/WX-", "-WX-"}) {
		return laneVerdict{toolchain: ToolchainMSVC, fatal: true, detail: "/WX is in force"}, true
	}
	return laneVerdict{toolchain: ToolchainMSVC, detail: "neither the command nor CL or _CL_ carries /WX"}, true
}

// compilesSource reports whether a compiler's args name a source file or one of compileOnly.
func compilesSource(args []string, compileOnly ...string) bool {
	for i := 0; i < len(args) && i < maxRunScriptFields; i++ {
		lower := strings.ToLower(args[i])
		for j := 0; j < len(cSourceSuffixes); j++ {
			if strings.HasSuffix(lower, cSourceSuffixes[j]) && !strings.HasPrefix(lower, "-") {
				return true
			}
		}
		if slices.Contains(compileOnly, args[i]) {
			return true
		}
	}
	return false
}

// lastSwitch reports whether the last of args that is one of on or off is one of on.
func lastSwitch(args, on, off []string) bool {
	state := false
	for i := 0; i < len(args) && i < maxRunScriptFields; i++ {
		switch {
		case slices.Contains(on, args[i]):
			state = true
		case slices.Contains(off, args[i]):
			state = false
		}
	}
	return state
}
