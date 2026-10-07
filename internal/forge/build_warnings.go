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

// The toolchains a build lane is named by (BuildLane.Toolchain). ToolchainUnreadCompiler names a
// command that compiles a source with a compiler the workflow gives as a variable or an
// expression whose value it does not show.
const (
	ToolchainGCCClang       = "gcc/clang"
	ToolchainMSVC           = "MSVC cl"
	ToolchainCMake          = "CMake"
	ToolchainMeson          = "Meson"
	ToolchainCargo          = "Cargo"
	ToolchainRustc          = "rustc"
	ToolchainGo             = "Go"
	ToolchainUnreadCompiler = "C/C++ compiler"
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

// BuildWarningsInputs are the facts MeasureBuildWarnings judges cargo lanes by that a package
// above this one reads: the Cargo.toml reader lives in internal/flavor, which imports forge.
type BuildWarningsInputs struct {
	// CargoLints is the level the [lints] tables of the root Cargo.toml's workspace give the
	// warnings lint group in every crate (flavor.CargoWarningsLints).
	CargoLints CargoLints
}

// CargoLints is the level every crate of the root Cargo.toml's workspace gives the warnings lint
// group in its [lints] table, directly or through [workspace.lints]: "deny", "forbid", or ""
// when some crate gives it neither or a manifest cannot be read. Detail says which, for a lane's
// detail. Cargo passes these levels to rustc before the extra flags of RUSTFLAGS and its other
// sources, so a later level there overrides a deny (https://doc.rust-lang.org/cargo/reference/manifest.html#the-lints-section).
type CargoLints struct {
	Level, Detail string
}

// MeasureBuildWarnings reads the run: steps of every workflow under repoPath's .github/workflows
// and names each command that compiles C, C++, Rust or Go code, with whether a warning fails it:
//
//   - gcc, clang and their cross, versioned and MinGW -posix/-win32 names: -Werror on the
//     command, not undone by a later -Wno-error. -Werror=<warning> makes that one warning an
//     error and does not count.
//   - MSVC cl: /WX (or -WX) on the command or in the CL or _CL_ variable, not undone by a later
//     /WX-; clang-cl also takes -Werror and -Wno-error.
//   - CMake, Meson, Cargo, rustc and Go: as their judges document (build_warnings_toolchains.go).
//   - A compiler the command gives as a variable is read from the variable's value; one whose
//     value the workflow does not show is a lane that fails as unread, never a skipped command.
//
// A variable is read from the command's own assignments, an export earlier in its script, then
// the step's, job's and workflow's env:, as far as sudo and env let it reach the program. A step
// whose failure does not bind (continue-on-error on it or its job) is never fatal, and a job or
// step whose if: is the literal false is not read. What a make target, a script, an action, a
// preset or a build file sets is not read; the repository's sources are read only for the
// languages a CMake lane compiles and the cgo files of a Go lane (readNativeSources).
func MeasureBuildWarnings(ctx context.Context, repoPath string, inputs BuildWarningsInputs) (BuildWarningsMeasurement, error) {
	if ctx == nil {
		return BuildWarningsMeasurement{}, errors.New("build warnings measurement requires a context")
	}
	ctx, cancel := context.WithTimeout(ctx, buildWarningsTimeout)
	defer cancel()
	files, err := readWorkflowFiles(ctx, repoPath)
	if err != nil {
		return BuildWarningsMeasurement{}, err
	}
	measure := buildMeasure{configured: map[string]bool{}, inputs: inputs}
	for i := 0; i < len(files) && i < maxWorkflowFiles; i++ {
		if err := measure.workflow(files[i].Name, files[i].Data); err != nil {
			return BuildWarningsMeasurement{}, fmt.Errorf("workflow %s: %w", files[i].Name, err)
		}
	}
	var sources nativeSources
	if len(measure.goLanes) > 0 || len(measure.cmakeLanes) > 0 {
		if sources, err = readNativeSources(ctx, repoPath); err != nil {
			return BuildWarningsMeasurement{}, err
		}
	}
	return BuildWarningsMeasurement{Lanes: measure.result(sources)}, nil
}

// buildMeasure collects the lanes of every workflow. goLanes and cmakeLanes are the lanes judged
// once every workflow and the repository's sources are read (result); vet is the first binding
// step that runs go vet; configured holds the jobs ("<workflow>/<job>/<toolchain>") a configure
// step of CMake or Meson ran in.
type buildMeasure struct {
	inputs     BuildWarningsInputs
	lanes      []BuildLane
	goLanes    []deferredLane
	cmakeLanes []deferredLane
	vet        string
	configured map[string]bool
}

// deferredLane is a lane whose verdict waits for the repository's sources: the index of the lane
// in buildMeasure.lanes and what its command set.
type deferredLane struct {
	index int
	cgo   goCgo
	cmake cmakeFlags
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

// laneSite is the step a command runs in, the environment it reads, and whether the step runs
// outside the checkout's root: a working-directory: or an earlier cd or pushd moved it.
type laneSite struct {
	lane     BuildLane
	advisory bool
	moved    bool
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
		moved:    slices.ContainsFunc([]string{run.Step.WorkingDirectory, run.Job.Defaults.Run.WorkingDirectory, spec.Defaults.Run.WorkingDirectory}, movesFromRoot),
		env:      stepEnvironment{scopes: []*yaml.Node{&run.Step.Env, &run.Job.Env, &spec.Env}, script: map[string]string{}},
	}
	commands := scriptCommands(fields)
	for i := 0; i < len(commands) && i < maxRunScriptFields; i++ {
		m.command(&site, parseShellCommand(commands[i]))
	}
	return nil
}

// movesFromRoot reports whether a working-directory: value names a directory other than the
// checkout's root.
func movesFromRoot(dir string) bool {
	dir = strings.TrimSpace(dir)
	return dir != "" && dir != "." && dir != "./"
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
	case "cd", "pushd":
		site.moved = true
		return
	}
	resolved, read := site.env.resolveProgram(cmd)
	args := site.env.expand(resolved.args)
	switch {
	case !read:
		m.unreadCompiler(site, cmd.word, args)
	case resolved.program == "go":
		m.goCommand(site, args, site.env.lookup(resolved))
	default:
		if verdict, ok := m.judge(site, resolved, args); ok {
			m.record(site, verdict)
		}
	}
}

// laneVerdict is what one judge decided about a command. A Go lane's cgo or a CMake lane's flags
// set defer the verdict to the repository's sources (result).
type laneVerdict struct {
	toolchain string
	fatal     bool
	detail    string
	cgo       *goCgo
	cmake     *cmakeFlags
}

// judge decides the command cmd with its expanded args, and reports false for a command that
// compiles nothing this measurement reads.
func (m *buildMeasure) judge(site *laneSite, cmd shellCommand, args []string) (laneVerdict, bool) {
	lookup := site.env.lookup(cmd)
	switch program := cmd.program; {
	case cCompilerName.MatchString(program):
		return judgeCCompiler(args)
	case program == "cl" || program == "clang-cl":
		return judgeMSVC(program, args, lookup)
	case program == "cmake":
		return m.judgeCMake(site, args, lookup)
	case program == "meson":
		return m.judgeMeson(site, args)
	case program == "cargo" || program == "cross":
		return m.judgeCargo(site, cmd, args, lookup)
	case program == "rustc":
		return judgeRustc(args)
	}
	return laneVerdict{}, false
}

// record appends the lane of a verdict at site. A step whose failure does not bind is never
// fatal, and its verdict is not deferred.
func (m *buildMeasure) record(site *laneSite, verdict laneVerdict) {
	lane := site.lane
	lane.Toolchain, lane.Fatal, lane.Detail = verdict.toolchain, verdict.fatal, verdict.detail
	lane.Remedy = buildRemedies[verdict.toolchain]
	switch {
	case site.advisory:
		lane.Fatal = false
		lane.Detail = "continue-on-error lets the step pass whatever its compiler reports (" + verdict.detail + ")"
	case verdict.cgo != nil:
		m.goLanes = append(m.goLanes, deferredLane{index: len(m.lanes), cgo: *verdict.cgo})
	case verdict.cmake != nil:
		m.cmakeLanes = append(m.cmakeLanes, deferredLane{index: len(m.lanes), cmake: *verdict.cmake})
	}
	m.lanes = append(m.lanes, lane)
}

// result judges the deferred lanes against the repository's sources and the first go vet step,
// now that every workflow is read.
func (m *buildMeasure) result(sources nativeSources) []BuildLane {
	for i := 0; i < len(m.goLanes) && i < len(m.lanes); i++ {
		lane := &m.lanes[m.goLanes[i].index]
		lane.Fatal, lane.Detail = judgeGoLane(m.vet, m.goLanes[i].cgo, sources)
	}
	for i := 0; i < len(m.cmakeLanes) && i < len(m.lanes); i++ {
		lane := &m.lanes[m.cmakeLanes[i].index]
		if fatal, detail := m.cmakeLanes[i].cmake.judge(sources); fatal {
			lane.Fatal, lane.Detail = true, detail
		} else if detail != "" {
			lane.Detail += "; " + detail
		}
	}
	return m.lanes
}

// unreadCompiler records a command whose program the workflow gives as a variable or an
// expression it shows no value for, when the command compiles a source: whether a warning fails
// it cannot be read, so it is a lane that fails and says so.
func (m *buildMeasure) unreadCompiler(site *laneSite, word string, args []string) {
	if !compilesSource(args, "-c", "/c") {
		return
	}
	m.record(site, laneVerdict{toolchain: ToolchainUnreadCompiler, detail: word + " names the compiler, and the " +
		"workflow shows no value for it, so whether -Werror or /WX applies cannot be read"})
}

// buildRemedies is the warnings-as-errors form each toolchain documents.
var buildRemedies = map[string]string{
	ToolchainGCCClang: "add -Werror to the command (gcc and clang: turn all warnings into errors; -Werror=<warning> covers that warning only)",
	ToolchainMSVC:     "add /WX to the command or to the CL environment variable (treat all compiler warnings as errors)",
	ToolchainCMake: "configure with -DCMAKE_COMPILE_WARNING_AS_ERROR=ON (CMake 3.24 or later) and without " +
		"--compile-no-warning-as-error",
	ToolchainMeson: "set the build directory up with meson setup --werror or -Dwerror=true",
	ToolchainCargo: `set RUSTFLAGS: "-D warnings" or CARGO_BUILD_WARNINGS: deny in the step's, job's or workflow's env:, ` +
		`deny warnings in the [lints.rust] table of every crate's Cargo.toml, or pass -- -D warnings to cargo clippy`,
	ToolchainRustc: "pass -D warnings to rustc",
	ToolchainGo: "run go vet ./... (or go test -vet=all) in a workflow step, the Go compiler itself reporting no warnings; " +
		"in a module with cgo files, also set CGO_CFLAGS: -Werror (and CGO_CXXFLAGS: -Werror for C++ files) in env:",
	ToolchainUnreadCompiler: "name the compiler on the command, or set the variable in env: to it, with its " +
		"warnings-as-errors form (-Werror for gcc and clang, /WX for cl)",
}

// cCompilerName matches the GCC and Clang drivers by base name, lower-cased and without .exe:
// gcc, g++, cc, c++, clang, clang++ and the clang-based Intel icx and icpx, with a target prefix
// such as x86_64-w64-mingw32-, a version suffix such as -14, and the -posix or -win32 thread
// model suffix of Debian's MinGW-w64 packages (x86_64-w64-mingw32-gcc-posix,
// x86_64-w64-mingw32-gcc-14-posix, x86_64-w64-mingw32-g++-win32:
// https://packages.debian.org/trixie/amd64/gcc-mingw-w64-x86-64-posix/filelist).
var cCompilerName = regexp.MustCompile(`^(?:[a-z0-9_.]+-)*(?:gcc|g\+\+|cc|c\+\+|clang|clang\+\+|icx|icpx)(?:-[0-9][0-9.]*)?(?:-posix|-win32)?$`)

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

// msvcSwitches are, per program, the switches that turn warnings as errors on and off. clang-cl
// also takes clang's -Werror and -Wno-error, the last switch of either spelling deciding:
// measured with clang-cl 23.1.1, -Werror /c, /WX /c and -WX /c exit 1 on a warning, -Werror
// -Wno-error, /WX -Wno-error, -Werror /WX- and -WX -WX- exit 0, and CL=/WX or _CL_=-Werror in
// the environment exit 1.
var msvcSwitches = map[string][2][]string{
	"cl":       {{"/WX", "-WX"}, {"/WX-", "-WX-"}},
	"clang-cl": {{"/WX", "-WX", "-Werror"}, {"/WX-", "-WX-", "-Wno-error"}},
}

// judgeMSVC decides a cl or clang-cl command: CL is prepended to its arguments and _CL_
// appended, and the last switch decides.
func judgeMSVC(program string, args []string, lookup lookupFunc) (laneVerdict, bool) {
	if !compilesSource(args, "/c", "-c") {
		return laneVerdict{}, false
	}
	prepended, _, _ := lookup("CL")
	appended, _, _ := lookup("_CL_")
	all := append(append(strings.Fields(prepended), args...), strings.Fields(appended)...)
	switches := msvcSwitches[program]
	if lastSwitch(all, switches[0], switches[1]) {
		return laneVerdict{toolchain: ToolchainMSVC, fatal: true, detail: "warnings as errors are in force (" + strings.Join(switches[0], ", ") + ")"}, true
	}
	return laneVerdict{toolchain: ToolchainMSVC, detail: "neither the command nor CL or _CL_ carries " + strings.Join(switches[0], " or ") + " in force"}, true
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
